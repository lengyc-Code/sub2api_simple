param(
    [string]$OutputDir = "output",
    [string]$BinaryName = "sub2api_simple"
)

$ErrorActionPreference = "Stop"

New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null

$binaryPath = Join-Path $OutputDir ($BinaryName + ".exe")
Write-Host "Building -> $binaryPath"
go build -o $binaryPath .

if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

Write-Host "Build completed: $binaryPath"

