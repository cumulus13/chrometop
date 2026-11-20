param(
    [string]$Addr = "127.0.0.1:9333",
    [string]$Token = "",
    [string]$CDP = "",
    [switch]$Run
)

$ErrorActionPreference = "Stop"

Write-Host "[ChromeTop] Syncing Go modules..." -ForegroundColor Cyan
go mod tidy

Write-Host "[ChromeTop] Formatting, vetting, and testing Go sources..." -ForegroundColor Cyan
gofmt -s -w .
go vet ./...
go test ./...

Write-Host "[ChromeTop] Building chrometop.exe..." -ForegroundColor Cyan
go build -trimpath -ldflags="-s -w" -o chrometop.exe ./cmd/chrometop

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
