param(
    [string]$Addr = "127.0.0.1:9333",
    [string]$Token = "",
    [string]$CDP = "",
    [switch]$Run
)

$ErrorActionPreference = "Stop"

# VERSION holds: version = "x.y.z"
$Version = "dev"
if (Test-Path "VERSION") {
    $match = Select-String -Path "VERSION" -Pattern 'version\s*=\s*"([^"]+)"'
    if ($match) { $Version = $match.Matches[0].Groups[1].Value }
}
$Commit = "none"
try { $Commit = (git rev-parse --short HEAD 2>$null) } catch { }
if (-not $Commit) { $Commit = "none" }

Write-Host "[ChromeTop] Syncing Go modules..." -ForegroundColor Cyan
go mod tidy

Write-Host "[ChromeTop] Formatting, vetting, and testing Go sources..." -ForegroundColor Cyan
gofmt -s -w .
go vet ./...
go test ./...

Write-Host "[ChromeTop] Building chrometop.exe ($Version, $Commit)..." -ForegroundColor Cyan
go build -trimpath -ldflags="-s -w -X main.version=$Version -X main.commit=$Commit" -o chrometop.exe ./cmd/chrometop

if ($Run) {
    $argsList = @("--addr", $Addr)
    if ($Token -ne "") {
        $argsList += @("--token", $Token)
    }
    if ($CDP -ne "") {
        $argsList += @("--cdp", $CDP)
    }
    & .\chrometop.exe @argsList
}
