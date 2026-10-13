# Live browse-play test against a real OpenList instance.
# Usage: .\scripts\browse-live-test.ps1 -Base http://127.0.0.1:5667 -StartDir "/Cartoon"
param(
  [string]$Base = "http://127.0.0.1:5667",
  [string]$StartDir = "/Cartoon",
  [string]$Password = "test1234"
)
$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.Net.Http

function RawReq([System.Net.Http.HttpClient]$hc, [string]$method, [string]$url, [hashtable]$headers) {
  $req = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::new($method), $url)
  if ($headers) {
    foreach ($k in $headers.Keys) { [void]$req.Headers.TryAddWithoutValidation($k, $headers[$k]) }
  }
  $resp = $hc.SendAsync($req).GetAwaiter().GetResult()
  $bytes = $resp.Content.ReadAsByteArrayAsync().GetAwaiter().GetResult()
  return @{ Status = [int]$resp.StatusCode; Bytes = $bytes; Resp = $resp }
}

function Show($k, $v) { Write-Host ("  {0,-38} {1}" -f $k, $v) }

# login
Invoke-RestMethod -Uri "$Base/api/auth/login" -Method Post -ContentType "application/json" -Body (@{ name = "admin"; password = $Password } | ConvertTo-Json) -SessionVariable sess | Out-Null
$tok = $sess.Cookies.GetCookies($Base)[0].Value
$handler = [System.Net.Http.HttpClientHandler]::new()
$handler.AllowAutoRedirect = $false
$handler.CookieContainer = New-Object System.Net.CookieContainer
$handler.CookieContainer.Add([uri]$Base, (New-Object System.Net.Cookie("doriyamedia_token", $tok, "/", "127.0.0.1")))
$hc = [System.Net.Http.HttpClient]::new($handler)
$hc.Timeout = [timespan]::FromSeconds(120)

function ListDir($p) {
  $r = RawReq $hc "GET" "$Base/api/browse/1?path=$([uri]::EscapeDataString($p))" @{}
  $j = [System.Text.Encoding]::UTF8.GetString($r.Bytes) | ConvertFrom-Json
  return $j.entries
}
function FindVideo($dir, $depth) {
  if ($depth -le 0) { return $null }
  $entries = ListDir $dir
  $v = $entries | Where-Object { $_.video } | Select-Object -First 1
  if ($v) { return $v.path }
  foreach ($e in ($entries | Where-Object { $_.is_dir } | Select-Object -First 6)) {
    $f = FindVideo $e.path ($depth - 1)
    if ($f) { return $f }
  }
  return $null
}

Write-Host "==> browse-play live test ($Base, $StartDir)"
$vp = FindVideo $StartDir 3
if (-not $vp) { Write-Host "no video found under $StartDir"; exit 1 }

$playResp = RawReq $hc "GET" "$Base/api/browseplay/1?path=$([uri]::EscapeDataString($vp))&client=ios" @{}
$play = [System.Text.Encoding]::UTF8.GetString($playResp.Bytes) | ConvertFrom-Json
Show "file_id" $play.file_id
Show "decision" "$($play.mode) | $($play.reason)"
Show "codec" "$($play.video_codec) / $($play.audio_codec)"
Show "duration" "$([math]::Round($play.duration,1))s"

Write-Host "==> fetch playback url"
if ($play.mode -eq "direct" -or $play.mode -eq "proxy") {
  $r = RawReq $hc "GET" ($Base + $play.url) @{ Range = "bytes=0-99" }
  if ($r.Status -eq 302) {
    $loc = $r.Resp.Headers.Location
    Show "302 host" $loc.Host
    $req = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::Get, $loc)
    [void]$req.Headers.TryAddWithoutValidation("Range", "bytes=0-99")
    $cresp = $hc.SendAsync($req).GetAwaiter().GetResult()
    Show "CDN range status" "$([int]$cresp.StatusCode) bytes=$($cresp.Content.Headers.ContentLength)"
    Show "content-range" $cresp.Content.Headers.ContentRange
    $cresp.Dispose()
  } else {
    Show "status" $r.Status
    Show "content-range" $r.Resp.Content.Headers.ContentRange
  }
} elseif ($play.mode -eq "remux") {
  $r = RawReq $hc "GET" ($Base + $play.url) @{}
  $m3u8 = [System.Text.Encoding]::UTF8.GetString($r.Bytes)
  $segs = ($m3u8 -split "`n" | Where-Object { $_ -match "^/" } | Select-Object -First 1)
  Show "playlist" "$($r.Status), first seg: $($segs.Trim())"
  if ($segs) {
    $s = RawReq $hc "GET" ($Base + $segs.Trim()) @{}
    $b = $s.Bytes
    $sync = $b[0] -eq 0x47
    Show "segment" "$($s.Status) bytes=$($b.Length) ts-sync=$sync"
  }
} elseif ($play.mode -eq "unsupported") {
  throw "the decision refused this file: $($play.reason)"
}
Write-Host "LIVE TEST DONE"
