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
# The session packager is pinned off for the same reason — a run that aborted
# before restoring it would otherwise change how steps 7 and 7b behave.
Invoke-RestMethod -Uri "$Base/api/config" -Method Put -WebSession $sess -ContentType "application/json" `
  -Body (J @{playback=@{preprobe_after_scan=$false; hls_session_packager=$false}}) | Out-Null
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

Write-Host "==> 7c. session packager: Apple-valid fMP4 for copied HEVC"
# Apple will not decode HEVC from MPEG-TS, so copied HEVC has to be fMP4 with a
# proper init segment. This is the whole point of the session packager.
Invoke-RestMethod -Uri "$Base/api/config" -Method Put -WebSession $sess -ContentType "application/json" `
  -Body (J @{playback=@{hls_session_packager=$true}}) | Out-Null
$sp = PlayDecision $hev1 "ios"
Show "decision with packager" "$($sp.mode) | $($sp.reason)"
if ($sp.mode -ne "remux") {
  throw "with the session packager on, HEVC on iOS should be copied (remux), got '$($sp.mode)'"
}
$spM3u8 = RawReq $hc "GET" "$Base$($sp.url)" @{}
$spTxt = [System.Text.Encoding]::UTF8.GetString($spM3u8.Bytes)
if ($spTxt -notmatch '#EXT-X-MAP:URI="([^"]+)"') { throw "session playlist has no EXT-X-MAP`n$spTxt" }
$initUrl = $Matches[1]
Show "playlist EXT-X-MAP" $initUrl
$initR = RawReq $hc "GET" "$Base$initUrl" @{}
$initTxt = [System.Text.Encoding]::ASCII.GetString($initR.Bytes)
Show "init fetch" "$($initR.Status) bytes=$($initR.Bytes.Length) hvc1=$($initTxt.Contains('hvc1')) hev1=$($initTxt.Contains('hev1'))"
# ffmpeg writes hev1 by default and Apple refuses to play that.
if (-not $initTxt.Contains('hvc1')) { throw "init segment is not hvc1, Apple would refuse it" }
if ($initTxt.Contains('hev1')) { throw "init segment still carries hev1" }

$spSegLine = ($spTxt -split "`n" | Where-Object { $_ -and $_ -notmatch '^#' } | Select-Object -First 1).Trim()
$spSeg = RawReq $hc "GET" "$Base$spSegLine" @{}
Show "session segment" "$($spSeg.Status) type=$($spSeg.ContentHeaders.ContentType) bytes=$($spSeg.Bytes.Length)"
if ($spSeg.Status -ne 200 -or $spSeg.Bytes.Length -eq 0) { throw "session segment fetch failed" }
$comb = Join-Path $env:TEMP "doriya-e2e-fmp4.mp4"
[System.IO.File]::WriteAllBytes($comb, $initR.Bytes + $spSeg.Bytes)
$decErr = (& ffmpeg -v error -i $comb -frames:v 1 -f null - 2>&1 | Out-String).Trim()
if ($decErr) { throw "fMP4 init+segment does not decode: $decErr" }
Show "fMP4 init+segment decodes" "OK"
Remove-Item $comb -ErrorAction SilentlyContinue

# The packaged output is cached so a replayed file needs no packaging at all.
# A finished session's playlist carries ENDLIST, so seeing it on a replay proves
# the output survived rather than being thrown away and packaged again.
Start-Sleep -Seconds 2
$replay = RawReq $hc "GET" "$Base$($sp.url)" @{}
$replayTxt = [System.Text.Encoding]::UTF8.GetString($replay.Bytes)
$replaySegs = @($replayTxt -split "`n" | Where-Object { $_ -and $_ -notmatch '^#' }).Count
Show "cached replay" "status=$($replay.Status) segments=$replaySegs endlist=$($replayTxt.Contains('#EXT-X-ENDLIST'))"
if ($replay.Status -ne 200 -or $replaySegs -eq 0) { throw "replaying a packaged file must be served from cache" }
if (-not $replayTxt.Contains('#EXT-X-ENDLIST')) {
  throw "the packaging should have finished by now; its output must be kept for replay"
}

Write-Host "==> 7d. resuming begins the session at the saved position"
# ffmpeg rebases timestamps to zero when it seeks, so a session that starts
# mid-file has to be put back on the absolute timeline when served: otherwise the
# player places the whole stream at zero and "continue watching" lands wrong.
# A different file than 7c on purpose: a fresh playback on a stream that has no
# session yet is exactly the case where the requested position must win.
$mkvFile = ($targets | Where-Object { $_.media.video.codec -eq "hevc" -and $_.media.audio.codec -eq "eac3" } | Select-Object -First 1).file
$dur = [double]$sp.duration
$resume = [math]::Round($dur * 0.5, 3)
Invoke-RestMethod -Uri "$Base/api/progress" -Method Post -WebSession $sess -ContentType "application/json" `
  -Body (J @{file_id=$mkvFile; position=$resume; duration=$dur}) | Out-Null
$rp = PlayDecision $mkvFile "ios"
Show "saved position" "$resume / $dur"
if ($rp.url -notmatch 'start=') {
  throw "the HLS URL must carry the saved position, got '$($rp.url)'"
}
# The client cannot see the session offset by itself: players present a packaged
# stream as starting at zero (hls.js re-aligns it), so /api/play has to say where
# the stream begins in file time, or progress and subtitles come out shifted.
$wantStart = [math]::Floor($resume / 4.0) * 4.0
Show "stream_start" "$($rp.stream_start) (期望 $wantStart)"
if ([math]::Abs([double]$rp.stream_start - $wantStart) -gt 0.001) {
  throw "stream_start must be the session's start position, got '$($rp.stream_start)' want $wantStart"
}
$rpM3u8 = RawReq $hc "GET" "$Base$($rp.url)" @{}
$rpTxt = [System.Text.Encoding]::UTF8.GetString($rpM3u8.Bytes)
if ($rpTxt -notmatch '#EXT-X-MEDIA-SEQUENCE:(\d+)') { throw "no MEDIA-SEQUENCE in the resumed playlist`n$rpTxt" }
$seq = [int]$Matches[1]
$segLen = 4.0
$wantSeq = [math]::Floor($resume / $segLen)
$wantPts = $seq * $segLen
Show "resumed playlist" "MEDIA-SEQUENCE=$seq (期望 $wantSeq)"
if ([math]::Abs($seq - $wantSeq) -gt 1) {
  throw "MEDIA-SEQUENCE $seq does not match the saved position $resume"
}
$rpSegLine = ($rpTxt -split "`n" | Where-Object { $_ -and $_ -notmatch '^#' } | Select-Object -First 1).Trim()
$rpSeg = RawReq $hc "GET" "$Base$rpSegLine" @{}
if ($rpTxt -notmatch '#EXT-X-MAP:URI="([^"]+)"') { throw "no EXT-X-MAP in the resumed playlist`n$rpTxt" }
$rpInit = RawReq $hc "GET" "$Base$($Matches[1])" @{}
$comb2 = Join-Path $env:TEMP "doriya-e2e-resume.mp4"
[System.IO.File]::WriteAllBytes($comb2, $rpInit.Bytes + $rpSeg.Bytes)
$ptsOut = ((& ffprobe -v error -select_streams v:0 -show_entries packet=pts_time -of csv=p=0 `
  -read_intervals '%+#1' $comb2 2>&1) | Out-String).Trim()
if (-not $ptsOut) { throw "ffprobe returned no pts for the resumed segment" }
$gotPts = [double](($ptsOut -split "`n")[0] -split ',')[0]
Show "resumed first video pts" "$gotPts (期望 ~$wantPts)"
if ([math]::Abs($gotPts - $wantPts) -gt 0.3) {
  throw "the resumed segment starts at ${gotPts}s, want ~${wantPts}s on the absolute timeline"
}
Remove-Item $comb2 -ErrorAction SilentlyContinue
# Leave no saved position behind for the remaining steps.
Invoke-RestMethod -Uri "$Base/api/progress" -Method Post -WebSession $sess -ContentType "application/json" `
  -Body (J @{file_id=$mkvFile; position=0; duration=$dur}) | Out-Null

Write-Host "==> 7e. with the packager on, a transcode is session-packaged too"
# The point of P2: one long-lived ffmpeg per playback rather than a process per
# segment — for transcodes as well, and the result is fMP4, not MPEG-TS.
# Forced, because with the packager on the HEVC fixtures legitimately copy
# instead of transcoding.
$tc = Invoke-RestMethod -Uri "$Base/api/play/$hev1`?client=ios&force=transcode" -WebSession $sess
Show "transcode decision" "$($tc.mode) | $($tc.reason)"
if ($tc.mode -ne "transcode") { throw "forced transcode must be honoured, got '$($tc.mode)'" }
$tcM3u8 = RawReq $hc "GET" "$Base$($tc.url)" @{}
$tcTxt = [System.Text.Encoding]::UTF8.GetString($tcM3u8.Bytes)
if ($tcTxt -notmatch '#EXT-X-MAP:URI="([^"]+)"') {
  throw "a transcoded session must still be fMP4 with an init segment`n$tcTxt"
}
$tcInit = RawReq $hc "GET" "$Base$($Matches[1])" @{}
$tcSegLine = ($tcTxt -split "`n" | Where-Object { $_ -and $_ -notmatch '^#' } | Select-Object -First 1).Trim()
$tcSeg = RawReq $hc "GET" "$Base$tcSegLine" @{}
Show "transcode session segment" "$($tcSeg.Status) type=$($tcSeg.ContentHeaders.ContentType) bytes=$($tcSeg.Bytes.Length)"
if ($tcSeg.ContentHeaders.ContentType -notmatch 'mp4') {
  throw "a session must serve fMP4 segments, got '$($tcSeg.ContentHeaders.ContentType)'"
}
$tcComb = Join-Path $env:TEMP "doriya-e2e-transcode.mp4"
[System.IO.File]::WriteAllBytes($tcComb, $tcInit.Bytes + $tcSeg.Bytes)
$tcCodec = ((& ffprobe -v error -select_streams v:0 -show_entries stream=codec_name -of csv=p=0 $tcComb 2>&1) | Out-String).Trim()
Show "transcode session codec" $tcCodec
if ($tcCodec -notmatch 'h264') { throw "a transcode must produce H.264, got '$tcCodec'" }
$tcErr = (& ffmpeg -v error -i $tcComb -frames:v 1 -f null - 2>&1 | Out-String).Trim()
if ($tcErr) { throw "transcoded session segment does not decode: $tcErr" }
Show "transcode session decodes" "OK"
Remove-Item $tcComb -ErrorAction SilentlyContinue

Write-Host "==> 7f. a seek outside the packaged window is still served"
# A session packages from the resume position forwards, so a viewer who then
# jumps back to the beginning asks for a segment that session will never produce.
# It has to start packaging from there, not wait for a segment that cannot come.
# Uses 7d's file, whose session is the one that started mid-file.
Invoke-RestMethod -Uri "$Base/api/progress" -Method Post -WebSession $sess -ContentType "application/json" `
  -Body (J @{file_id=$mkvFile; position=$resume; duration=$dur}) | Out-Null
$rs = PlayDecision $mkvFile "ios"
$rsM3u8 = RawReq $hc "GET" "$Base$($rs.url)" @{}
$rsTxt = [System.Text.Encoding]::UTF8.GetString($rsM3u8.Bytes)
if ($rsTxt -notmatch '#EXT-X-MEDIA-SEQUENCE:(\d+)') { throw "no MEDIA-SEQUENCE in the resumed playlist`n$rsTxt" }
$rsSeq = [int]$Matches[1]
if ($rsSeq -lt 1) { throw "expected this session to start past the beginning, got $rsSeq" }
$rsSegUrl = ($rsTxt -split "`n" | Where-Object { $_ -and $_ -notmatch '^#' } | Select-Object -First 1).Trim()
$wantUrl = $rsSegUrl -replace 'seg\d+\.m4s', 'seg00000.m4s'
Show "session starts at" "segment $rsSeq; asking for seg00000"
$rsSeg = RawReq $hc "GET" "$Base$wantUrl" @{}
Show "seek-back segment" "$($rsSeg.Status) type=$($rsSeg.ContentHeaders.ContentType) bytes=$($rsSeg.Bytes.Length)"
if ($rsSeg.Status -ne 200 -or $rsSeg.Bytes.Length -eq 0) {
  throw "a seek before the session start must be served, got status $($rsSeg.Status)"
}
# Fetch the init afterwards, so it comes from the session just started at 0.
if ($rsTxt -notmatch '#EXT-X-MAP:URI="([^"]+)"') { throw "no EXT-X-MAP in the resumed playlist" }
$rsInit = RawReq $hc "GET" "$Base$($Matches[1])" @{}
$rsComb = Join-Path $env:TEMP "doriya-e2e-seekback.mp4"
[System.IO.File]::WriteAllBytes($rsComb, $rsInit.Bytes + $rsSeg.Bytes)
$rsErr = (& ffmpeg -v error -i $rsComb -frames:v 1 -f null - 2>&1 | Out-String).Trim()
if ($rsErr) { throw "seek-back segment does not decode: $rsErr" }
Show "seek-back segment decodes" "OK"
Remove-Item $rsComb -ErrorAction SilentlyContinue
# Leave no saved position behind for the remaining steps.
Invoke-RestMethod -Uri "$Base/api/progress" -Method Post -WebSession $sess -ContentType "application/json" `
  -Body (J @{file_id=$hev1; position=0; duration=$dur}) | Out-Null

# Back to the default so the remaining steps see ordinary behaviour.
Invoke-RestMethod -Uri "$Base/api/config" -Method Put -WebSession $sess -ContentType "application/json" `
  -Body (J @{playback=@{hls_session_packager=$false}}) | Out-Null

Write-Host "==> 8. subtitles"
$epFile = ($targets | Where-Object { $_.title -match "^E1 " } | Select-Object -First 1).file
$playEp = PlayDecision $epFile "ios"
foreach ($sub in $playEp.subtitles) {
  Show "subtitle" "$($sub.label) -> $($sub.url)$(if ($sub.default) { '  [默认]' })"
}
if (@($playEp.subtitles).Count -gt 0) {
  # The client turns subtitles on by itself, so the server has to say which one
  # is worth showing; otherwise every client invents its own rule.
  $marked = @($playEp.subtitles | Where-Object { $_.default })
  Show "default subtitle id" "$($playEp.default_subtitle_id)"
  if ($marked.Count -ne 1) {
    throw "exactly one subtitle must be marked default, got $($marked.Count)"
  }
  if ($marked[0].id -ne $playEp.default_subtitle_id) {
    throw "the default flag and default_subtitle_id disagree"
  }
  # The test episode carries a Chinese sidecar; that is what a viewer reads.
  if ($marked[0].lang -ne "chi") {
    throw "the Chinese sidecar should win, got lang='$($marked[0].lang)'"
  }
  $vtt = RawReq $hc "GET" "$Base$($playEp.subtitles[0].url)" @{}
  $vtxt = [System.Text.Encoding]::UTF8.GetString($vtt.Bytes)
  Show "vtt head" ((($vtxt -split "`n")[0..2]) -join " / ")
  Show "vtt cue timing converted" ($vtxt -match "00:00:00\.500 --> 00:00:02\.000")
  # A cue that is timed in file time has to move when the stream does not start at
  # the file's beginning, and it must not come out as an impossible ":60.000".
  $shifted = RawReq $hc "GET" "$Base$($playEp.subtitles[0].url)&start=0.200" @{}
  $shiftedTxt = [System.Text.Encoding]::UTF8.GetString($shifted.Bytes)
  Show "vtt shifted by 0.2s" "$($shiftedTxt -match '00:00:00\.300 --> 00:00:01\.800')"
  if ($shiftedTxt -notmatch '00:00:00\.300 --> 00:00:01\.800') {
    throw "a shifted rendition's cues must move with the stream`n$shiftedTxt"
  }
  # Shifted cues belong to one playback, so they must not be cached as the
  # unshifted document is. (Cache-Control is a response header, not a content one,
  # and PowerShell does not surface HttpResponseHeaders' indexer.)
  $cc = ($shifted.RespHeaders.GetValues('Cache-Control') -join ',')
  Show "shifted vtt cache-control" $cc
  if ($cc -notmatch 'no-store') {
    throw "shifted cues must be served no-store, got '$cc'"
  }
}

Write-Host "==> 8b. HLS subtitle renditions (players that manage their own)"
# Safari plays HLS natively and ignores <track>, so the same subtitles are offered
# as EXT-X-MEDIA renditions behind a master playlist. Forced to transcode because
# a Safari client would otherwise pick a direct/proxy stream, which has no playlist.
$epForce = Invoke-RestMethod -Uri "$Base/api/play/$epFile`?client=ios&force=transcode" -WebSession $sess
Show "url for a Safari client" "$($epForce.url -replace '\?.*','')"
if ($epForce.url -notmatch '/master\.m3u8\?') {
  throw "a Safari client with subtitles must get the master playlist, got '$($epForce.url)'"
}
if (-not $epForce.native_subtitles) {
  throw "native_subtitles must tell the page the player owns the subtitles"
}
$master = RawReq $hc "GET" "$Base$($epForce.url)" @{}
$masterTxt = [System.Text.Encoding]::UTF8.GetString($master.Bytes)
$media = @($masterTxt -split "`n" | Where-Object { $_ -match '^#EXT-X-MEDIA:TYPE=SUBTITLES' })
$defaults = @($media | Where-Object { $_ -match 'DEFAULT=YES' })
Show "master renditions" "$($media.Count) (默认 $($defaults.Count))"
if ($media.Count -lt 1) { throw "the master playlist lists no subtitle renditions`n$masterTxt" }
if ($defaults.Count -ne 1) { throw "exactly one rendition must be DEFAULT, got $($defaults.Count)" }
if ($masterTxt -notmatch '#EXT-X-STREAM-INF:.*SUBTITLES="subs"') {
  throw "the variant must declare the subtitle group`n$masterTxt"
}
if ($masterTxt -notmatch 'URI="(/api/hls/\d+/subs/\d+\.m3u8[^"]*)"') {
  throw "a rendition must point at its own playlist`n$masterTxt"
}
$subPlaylist = RawReq $hc "GET" "$Base$($Matches[1])" @{}
$subTxt = [System.Text.Encoding]::UTF8.GetString($subPlaylist.Bytes)
Show "rendition playlist" "status=$($subPlaylist.Status) extinf=$($subTxt -match '#EXTINF:') endlist=$($subTxt.Contains('#EXT-X-ENDLIST'))"
if ($subTxt -notmatch '#EXTINF:[\d.]+,' -or -not $subTxt.Contains('#EXT-X-ENDLIST')) {
  throw "a rendition playlist needs one segment and an ENDLIST`n$subTxt"
}
if ($subTxt -notmatch '(/api/subs/\d+/\d+\.vtt[^\s]*\.vtt|/api/subs/\d+/\d+\.vtt[^\s]*)') {
  throw "the rendition playlist must point at the WebVTT document`n$subTxt"
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
}

# Our own proxy has to honour a range for a remote source as well: that is what
# lets a player seek while the server does no transcoding at all. What an upstream
# does when it *ignores* a range (the proxy answers it itself) is covered by the
# handler tests in internal/api/proxy_range_test.go, which drive real HTTP.
$prng = RawReq $hc "GET" "$Base/api/stream/$ofile`?proxy=1" @{Range="bytes=100-199"}
$pcr = "$($prng.ContentHeaders.ContentRange)"
Show "proxy range (cloud)" "status=$($prng.Status) len=$($prng.Bytes.Length) cr=$pcr"
if ($prng.Status -ne 206) { throw "the proxy must answer a range with 206, got $($prng.Status)" }
if ($prng.Bytes.Length -ne 100) { throw "the proxy returned $($prng.Bytes.Length) bytes, want 100" }
if ($pcr -notmatch '^bytes 100-199/\d+$') { throw "the proxy's Content-Range is wrong: '$pcr'" } else {
  Show "cloud play url" $oplay.url
}

Write-Host "==> 10. progress save"
Invoke-RestMethod -Uri "$Base/api/progress" -Method Post -WebSession $sess -ContentType "application/json" -Body (J @{file_id=$h264File; position=42.5; duration=100}) | Out-Null
$homeData = Invoke-RestMethod -Uri "$Base/api/home" -WebSession $sess
Show "continue watching entries" (@($homeData.continue_watching).Count)

Write-Host ""
Write-Host "E2E DONE" -ForegroundColor Green
