# End-to-end smoke test for DoriyaMedia (requires a running server + mock openlist).
# Usage: .\scripts\e2e-test.ps1 [-Base "http://127.0.0.1:5666"] [-Password "test1234"]
param(
  [string]$Base = "http://127.0.0.1:5666",
  [string]$Password = "test1234",
  [string]$TestData = "..\testdata",
  [string]$MockBase = "http://127.0.0.1:5699"
)

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

function J($o) { $o | ConvertTo-Json -Depth 8 -Compress }
function Show($label, $val) { Write-Host ("  {0,-38} {1}" -f $label, $val) }

Write-Host "==> 1. setup/login"
try {
  $r = Invoke-RestMethod -Uri "$Base/api/setup" -Method Post -Body (J @{name="admin"; password=$Password}) -ContentType "application/json" -SessionVariable sess
  Show "setup" "created admin"
} catch {
  $r = Invoke-RestMethod -Uri "$Base/api/auth/login" -Method Post -Body (J @{name="admin"; password=$Password}) -ContentType "application/json" -SessionVariable sess
  Show "setup skipped, login" "ok"
}
$token = $r.token

# raw http client (cookie auth, redirect control) for low-level checks
Add-Type -AssemblyName System.Net.Http
$cookieBox = New-Object System.Net.CookieContainer
$hRedirect = New-Object System.Net.Http.HttpClientHandler
$hRedirect.CookieContainer = $cookieBox
$hRedirect.AllowAutoRedirect = $true
$hc = New-Object System.Net.Http.HttpClient($hRedirect)
$hc.Timeout = [TimeSpan]::FromSeconds(180)
$hNoRedir = New-Object System.Net.Http.HttpClientHandler
$hNoRedir.CookieContainer = $cookieBox
$hNoRedir.AllowAutoRedirect = $false
$hcNoRedir = New-Object System.Net.Http.HttpClient($hNoRedir)
$hcNoRedir.Timeout = [TimeSpan]::FromSeconds(180)
$cookieBox.Add((New-Object System.Uri($Base)), (New-Object System.Net.Cookie("doriyamedia_token", $token, "/")))

function RawReq($client, $method, $url, $headers) {
  $req = New-Object System.Net.Http.HttpRequestMessage((New-Object System.Net.Http.HttpMethod($method)), $url)
  foreach ($k in @($headers.Keys)) { [void]$req.Headers.TryAddWithoutValidation($k, $headers[$k]) }
  $resp = $client.SendAsync($req).GetAwaiter().GetResult()
  $bytes = $resp.Content.ReadAsByteArrayAsync().GetAwaiter().GetResult()
  return @{ Status = [int]$resp.StatusCode; RespHeaders = $resp.Headers; ContentHeaders = $resp.Content.Headers; Bytes = $bytes }
}

# /api/play answers immediately with state="probing" while the file's first
# probe runs in the background (probing a cloud file can take minutes); poll the
# status endpoint, then re-request the decision. Mirrors what the player does.
function PlayDecision($fileId, $client) {
  $url = "$Base/api/play/$fileId`?client=$client"
  $r = Invoke-RestMethod -Uri $url -WebSession $sess
  $deadline = (Get-Date).AddSeconds(180)
  while ($r.state -eq "probing" -and (Get-Date) -lt $deadline) {
    Start-Sleep -Milliseconds 400
    $st = Invoke-RestMethod -Uri "$Base/api/play/$fileId/status" -WebSession $sess
    if ($st.state -eq "failed") { throw "probe failed for file $fileId`: $($st.error)" }
    if ($st.state -eq "ready") { $r = Invoke-RestMethod -Uri $url -WebSession $sess }
  }
  if ($r.state -eq "probing") { throw "probe for file $fileId did not finish within 180s" }
  return $r
}

Write-Host "==> 2. create local source + libraries"
$root = (Resolve-Path $TestData).Path
$src = Invoke-RestMethod -Uri "$Base/api/sources" -Method Post -WebSession $sess -ContentType "application/json" -Body (J @{name="本地测试"; type="local"; config=@{root=$root}})
Show "source id" $src.id

$t = Invoke-RestMethod -Uri "$Base/api/sources/test" -Method Post -WebSession $sess -ContentType "application/json" -Body (J @{type="local"; config=@{root=$root}})
Show "source test entries" ($t.entries -join ", ")

$libMovie = Invoke-RestMethod -Uri "$Base/api/libraries" -Method Post -WebSession $sess -ContentType "application/json" -Body (J @{name="测试电影"; type="movie"; source_id=$src.id; path="/movies"})
$libSeries = Invoke-RestMethod -Uri "$Base/api/libraries" -Method Post -WebSession $sess -ContentType "application/json" -Body (J @{name="测试剧集"; type="series"; source_id=$src.id; path="/series"})
Show "libraries" "$($libMovie.id), $($libSeries.id)"

Write-Host "==> 3. scan (post-scan pre-probe OFF so the async path is observable)"
# The scan below must NOT warm the probe cache, otherwise step 5a would find a
# cached probe and the async contract would go untested. Set it explicitly: the
# server persists config, so relying on the file's current value is not enough.
Invoke-RestMethod -Uri "$Base/api/config" -Method Put -WebSession $sess -ContentType "application/json" `
  -Body (J @{playback=@{preprobe_after_scan=$false}}) | Out-Null
$pre0 = (Invoke-RestMethod -Uri "$Base/api/config" -WebSession $sess).playback.preprobe_after_scan
Show "preprobe_after_scan (before scan)" $pre0
if ($pre0) { throw "could not disable pre-probe for the async probe check" }

Invoke-RestMethod -Uri "$Base/api/libraries/$($libMovie.id)/scan" -Method Post -WebSession $sess | Out-Null
Invoke-RestMethod -Uri "$Base/api/libraries/$($libSeries.id)/scan" -Method Post -WebSession $sess | Out-Null
$deadline = (Get-Date).AddSeconds(180)
do {
  Start-Sleep -Milliseconds 800
  $st = Invoke-RestMethod -Uri "$Base/api/scan/status" -WebSession $sess
  $mstate = @($st | Where-Object { $_.library_id -eq $libMovie.id })
  $sstate = @($st | Where-Object { $_.library_id -eq $libSeries.id })
  $allDone = ($mstate.Count -gt 0 -and $mstate[0].phase -eq "done" -and -not $mstate[0].running) -and
             ($sstate.Count -gt 0 -and $sstate[0].phase -eq "done" -and -not $sstate[0].running)
} while (-not $allDone -and (Get-Date) -lt $deadline)
$st | ForEach-Object { Show "scan lib $($_.library_id)" "$($_.phase) done=$($_.done) total=$($_.total) err=$($_.error)" }

Write-Host "==> 4. parsed items"
$movies = Invoke-RestMethod -Uri "$Base/api/items?library_id=$($libMovie.id)&type=movie" -WebSession $sess
$movies.items | ForEach-Object { Show "movie" "$($_.name) ($($_.year))" }
$shows = Invoke-RestMethod -Uri "$Base/api/items?library_id=$($libSeries.id)&type=show" -WebSession $sess
$shows.items | ForEach-Object { Show "show" $_.name }

Write-Host "==> 5. playback decisions (iOS vs desktop)"
$targets = @()
foreach ($m in $movies.items) {
  $d = Invoke-RestMethod -Uri "$Base/api/items/$($m.id)" -WebSession $sess
  foreach ($f in $d.files) { $targets += @{title=$m.name; file=$f.id; media=$null} }
}
$showDetail = Invoke-RestMethod -Uri "$Base/api/items/$($shows.items[0].id)" -WebSession $sess
foreach ($season in $showDetail.seasons) {
  foreach ($ep in $season.episodes) {
    $ed = Invoke-RestMethod -Uri "$Base/api/items/$($ep.id)" -WebSession $sess
    foreach ($f in $ed.files) { $targets += @{title="E$($ep.episode) $($ep.name)"; file=$f.id; media=$null} }
  }
}

Write-Host "==> 5a. async probe contract (config-test has post-scan pre-probe off)"
# No cached probe exists for this file, so /api/play must answer immediately
# with state=probing instead of blocking until ffprobe returns.
$asyncFile = $targets[0].file
$sw = [System.Diagnostics.Stopwatch]::StartNew()
$p0 = Invoke-RestMethod -Uri "$Base/api/play/$asyncFile`?client=ios" -WebSession $sess
$sw.Stop()
Show "first /api/play answer" "$([int]$sw.ElapsedMilliseconds)ms state=$($p0.state) url='$($p0.url)'"
Show "preparing reason" $p0.reason
if ($p0.state -eq "probing") {
  if ($p0.url) { throw "state=probing must not carry a playback url" }
  $st0 = Invoke-RestMethod -Uri "$Base/api/play/$asyncFile/status" -WebSession $sess
  Show "status right after" "state=$($st0.state) elapsed=$([math]::Round([double]$st0.elapsed,2))s"
} else {
  Write-Host "  NOTE: probe was already cached (state=$($p0.state))" -ForegroundColor Yellow
}
$p1 = PlayDecision $asyncFile "ios"
Show "after polling -> mode" "$($p1.mode) | $($p1.reason)"
Show "status is now ready" "$((Invoke-RestMethod -Uri "$Base/api/play/$asyncFile/status" -WebSession $sess).state)"

Write-Host "==> 5b. enable post-scan pre-probe for the cloud library check"
Invoke-RestMethod -Uri "$Base/api/config" -Method Put -WebSession $sess -ContentType "application/json" `
  -Body (J @{playback=@{preprobe_after_scan=$true; preprobe_max_files=50; preprobe_concurrency=2}}) | Out-Null
$cfgNow = Invoke-RestMethod -Uri "$Base/api/config" -WebSession $sess
Show "preprobe_after_scan" $cfgNow.playback.preprobe_after_scan

foreach ($t in $targets) {
  $ios = PlayDecision $t.file "ios"
  $pc  = PlayDecision $t.file "chrome"
  $t.media = $ios.media
  $vid = "-"; if ($t.media -and $t.media.video) { $vid = "$($t.media.video.codec)/$($t.media.video.tag)" }
  $aud = "-"; if ($t.media -and $t.media.audio.Count -gt 0) { $aud = $t.media.audio[0].codec }
  Write-Host ("  [{0}] v={1} a={2}" -f $t.title, $vid, $aud)
  Show "  ios ->" "$($ios.mode) | $($ios.reason)"
  Show "  desktop ->" "$($pc.mode) | $($pc.reason)"
}

Write-Host "==> 6. stream endpoint checks"
$h264File = ($targets | Where-Object { $_.media.video.codec -eq "h264" } | Select-Object -First 1).file
$resp = RawReq $hc "GET" "$Base/api/stream/$($h264File)" @{Range="bytes=0-99"}
Show "local proxy range status" "$($resp.Status) len=$($resp.Bytes.Length) cr=$($resp.ContentHeaders.ContentRange)"
$resp = RawReq $hc "HEAD" "$Base/api/stream/$($h264File)" @{}
Show "local HEAD status" "$($resp.Status) accept-ranges=$($resp.RespHeaders.AcceptRanges)"

Write-Host "==> 7. HLS remux pipeline (hevc_hev1 file, iOS)"
$hev1 = ($targets | Where-Object { $_.media.video.tag -eq "hev1" } | Select-Object -First 1).file
$play = PlayDecision $hev1 "ios"
Show "decision" "$($play.mode) $($play.url)"
$m3u8r = RawReq $hc "GET" "$Base$($play.url)" @{}
$txt = [System.Text.Encoding]::UTF8.GetString($m3u8r.Bytes)
$lines = $txt -split "`n"
$segLine = ($lines | Where-Object { $_ -and $_ -notmatch '^#' } | Select-Object -First 1).Trim()
Show "playlist status" "$($m3u8r.Status) type=$($m3u8r.ContentHeaders.ContentType)"
Show "playlist segments" (($lines | Where-Object { $_ -match '^#EXTINF' }).Count)
Show "first segment url" $segLine
$seg = RawReq $hc "GET" "$Base$segLine" @{}
Show "segment fetch" "$($seg.Status) type=$($seg.ContentHeaders.ContentType) bytes=$($seg.Bytes.Length)"
Show "TS sync byte (0x47)" ($seg.Bytes.Length -gt 188 -and $seg.Bytes[0] -eq 0x47)

# A segment can be a structurally valid TS and still carry no decodable video
# (that is exactly what "audio but no picture" looks like from the client side),
# so decode a frame instead of only checking the container.
$segPath = Join-Path $env:TEMP "doriya-e2e-seg.ts"
[System.IO.File]::WriteAllBytes($segPath, $seg.Bytes)
$vstreams = (& ffprobe -v error -select_streams v -show_entries stream=codec_name -of csv=p=0 $segPath 2>&1 | Out-String).Trim()
Show "segment video stream" $(if ($vstreams) { $vstreams } else { "NONE" })
$decErr = (& ffmpeg -v error -i $segPath -frames:v 1 -f null - 2>&1 | Out-String).Trim()
if ($decErr) {
  Show "segment video decodes" "FAIL -> $decErr"
  throw "first HLS segment has no decodable video: $decErr"
} else {
  Show "segment video decodes" "OK (a frame decoded)"
}
Remove-Item $segPath -ErrorAction SilentlyContinue

$seg2 = RawReq $hc "GET" "$Base$($segLine -replace '/0\?', '/3?')" @{}
Show "seek segment 3 fetch" "$($seg2.Status) bytes=$($seg2.Bytes.Length)"

# Generation progress must be observable, so the player can show a real bar
# instead of an opaque spinner.
$pst = Invoke-RestMethod -Uri "$Base/api/play/$hev1/status" -WebSession $sess
if ($pst.stream) {
  Show "generation progress" "ready=$($pst.stream.segments_ready) generated=$([math]::Round([double]$pst.stream.generated_seconds,1))s rate=$([math]::Round([double]$pst.stream.realtime_factor,2))x"
} else {
  Show "generation progress" "MISSING (status carried no stream block)"
  throw "status endpoint did not report segment-generation progress"
}

Write-Host "==> 7b. forcing a remux must not copy undecodable video"
# hls.js cannot demux HEVC from MPEG-TS, so a forced remux for an MSE client
# would produce audio with no picture. It must fall back to a real transcode.
$forced = Invoke-RestMethod -Uri "$Base/api/play/$hev1`?client=chrome&force=remux" -WebSession $sess
Show "forced remux (chrome, HEVC)" "$($forced.mode) | $($forced.reason)"
if ($forced.mode -ne "transcode") {
  throw "forced remux for an MSE client on HEVC must fall back to transcode, got '$($forced.mode)'"
}
if ($forced.reason -notmatch 'HEVC') {
  throw "the fallback reason must name the codec, got '$($forced.reason)'"
}

Write-Host "==> 8. subtitles"
$epFile = ($targets | Where-Object { $_.title -match "^E1 " } | Select-Object -First 1).file
$playEp = PlayDecision $epFile "ios"
foreach ($sub in $playEp.subtitles) { Show "subtitle" "$($sub.label) -> $($sub.url)" }
if (@($playEp.subtitles).Count -gt 0) {
  $vtt = RawReq $hc "GET" "$Base$($playEp.subtitles[0].url)" @{}
  $vtxt = [System.Text.Encoding]::UTF8.GetString($vtt.Bytes)
  Show "vtt head" ((($vtxt -split "`n")[0..2]) -join " / ")
  Show "vtt cue timing converted" ($vtxt -match "00:00:00\.500 --> 00:00:02\.000")
}

Write-Host "==> 9. openlist source (mock) + 302 direct"
$osrc = Invoke-RestMethod -Uri "$Base/api/sources" -Method Post -WebSession $sess -ContentType "application/json" -Body (J @{name="Mock OpenList"; type="openlist"; config=@{base_url=$MockBase}})
$olib = Invoke-RestMethod -Uri "$Base/api/libraries" -Method Post -WebSession $sess -ContentType "application/json" -Body (J @{name="网盘电影"; type="movie"; source_id=$osrc.id; path="/movies"})
Invoke-RestMethod -Uri "$Base/api/libraries/$($olib.id)/scan" -Method Post -WebSession $sess | Out-Null
$deadline = (Get-Date).AddSeconds(180)
do {
  Start-Sleep -Milliseconds 800
  $st = Invoke-RestMethod -Uri "$Base/api/scan/status" -WebSession $sess
  $ostate = @($st | Where-Object { $_.library_id -eq $olib.id })
  $oDone = $ostate.Count -gt 0 -and $ostate[0].phase -eq "done" -and -not $ostate[0].running
} while (-not $oDone -and (Get-Date) -lt $deadline)
$omovies = Invoke-RestMethod -Uri "$Base/api/items?library_id=$($olib.id)&type=movie" -WebSession $sess
Show "openlist movies parsed" $omovies.total
$od = Invoke-RestMethod -Uri "$Base/api/items/$($omovies.items[0].id)" -WebSession $sess
$ofile = $od.files[0].id
# Pre-probe was enabled before this scan and no /api/play call has touched this
# file, so state=ready can only mean the background pre-probe probed and
# persisted it — i.e. the cache was warmed without the user waiting.
$preDeadline = (Get-Date).AddSeconds(60)
$pst = $null
do {
  Start-Sleep -Milliseconds 500
  $pst = Invoke-RestMethod -Uri "$Base/api/play/$ofile/status" -WebSession $sess
} while ($pst.state -ne "ready" -and (Get-Date) -lt $preDeadline)
Show "pre-probed before any play" "state=$($pst.state)"
$sw2 = [System.Diagnostics.Stopwatch]::StartNew()
$oplay = Invoke-RestMethod -Uri "$Base/api/play/$ofile`?client=ios" -WebSession $sess
$sw2.Stop()
Show "first decision (warm cache)" "$([int]$sw2.ElapsedMilliseconds)ms state=$($oplay.state) mode=$($oplay.mode)"
if ($oplay.state -eq "probing") { $oplay = PlayDecision $ofile "ios" }
Show "cloud play decision" "$($oplay.mode) | $($oplay.reason)"
if ($oplay.mode -eq "direct") {
  $r302 = RawReq $hcNoRedir "GET" "$Base$($oplay.url)" @{}
  $loc = $r302.RespHeaders.Location
  if ($loc -is [array]) { $loc = $loc[0] }
  Show "redirect status" $r302.Status
  Show "302 location" $loc
  $rcdn = RawReq $hcNoRedir "GET" $loc @{Range="bytes=0-99"}
  Show "CDN range follow" "$($rcdn.Status) len=$($rcdn.Bytes.Length)"
} else {
  Show "cloud play url" $oplay.url
}

Write-Host "==> 10. progress save"
Invoke-RestMethod -Uri "$Base/api/progress" -Method Post -WebSession $sess -ContentType "application/json" -Body (J @{file_id=$h264File; position=42.5; duration=100}) | Out-Null
$homeData = Invoke-RestMethod -Uri "$Base/api/home" -WebSession $sess
Show "continue watching entries" (@($homeData.continue_watching).Count)

Write-Host ""
Write-Host "E2E DONE" -ForegroundColor Green
