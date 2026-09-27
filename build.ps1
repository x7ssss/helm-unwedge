$ErrorActionPreference = "Stop"

$version = if ($env:VERSION) { $env:VERSION } else { "0.1.0" }
$distDir = "dist"

if (-not (Test-Path $distDir)) {
    New-Item -ItemType Directory -Path $distDir | Out-Null
}

$platforms = @(
    @{ OS = "linux"; Arch = "amd64"; Ext = "" },
    @{ OS = "linux"; Arch = "arm64"; Ext = "" },
    @{ OS = "darwin"; Arch = "amd64"; Ext = "" },
    @{ OS = "darwin"; Arch = "arm64"; Ext = "" },
    @{ OS = "windows"; Arch = "amd64"; Ext = ".exe" }
)

$ldflags = "-s -w"

Write-Host "Building helm-unwedge binaries into $distDir..."

foreach ($p in $platforms) {
    $targetName = "helm-unwedge-$($p.OS)-$($p.Arch)$($p.Ext)"
    $outputPath = Join-Path $distDir $targetName
    Write-Host "Compiling $targetName..."
    
    $env:GOOS = $p.OS
    $env:GOARCH = $p.Arch
    $env:CGO_ENABLED = "0"
    
    go build -trimpath -ldflags=$ldflags -o $outputPath ./cmd/helm-unwedge
}

Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue

Write-Host "Build finished. Dist artifacts:"
Get-ChildItem $distDir | Select-Object Name, Length
