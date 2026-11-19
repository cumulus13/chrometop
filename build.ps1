param(
    [string]$Addr = "127.0.0.1:9333",
    [string]$Token = "",
    [switch]$Run
)

$ErrorActionPreference = "Stop"

Write-Host "[ChromeTop] Formatting, vetting, and testing Go sources..." -ForegroundColor Cyan
gofmt -s -w .
go vet ./...
go test ./...

Write-Host "[ChromeTop] Building chrometop.exe..." -ForegroundColor Cyan
go build -trimpath -ldflags="-s -w" -o chrometop.exe ./cmd/chrometop

if ($Run) {
    if ($Token -ne "") {
        .\chrometop.exe --addr $Addr --token $Token
    } else {
        .\chrometop.exe --addr $Addr
    }
}
