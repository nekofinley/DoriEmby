# Build Doriya: frontend -> embed -> single binary
param(
  [string]$Output = "doriya.exe"
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path | Split-Path -Parent

Write-Host "==> building webui" -ForegroundColor Cyan
Push-Location "$root\webui"
try {
  if (-not (Test-Path node_modules)) { pnpm install }
  pnpm run build
} finally {
  Pop-Location
}

Write-Host "==> building server (embeds webui/dist)" -ForegroundColor Cyan
$env:GOPROXY = "https://goproxy.cn,https://proxy.golang.org,direct"
Push-Location $root
try {
  go build -ldflags "-s -w" -o $Output ./cmd/doriya
  if ($LASTEXITCODE -ne 0) { throw "go build failed" }
} finally {
  Pop-Location
}
Write-Host "==> done: $Output" -ForegroundColor Green
