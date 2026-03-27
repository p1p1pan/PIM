param()

$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$pidFile = Join-Path $scriptDir ".port-forward.pids.json"

if (-not (Test-Path $pidFile)) {
    Write-Host "PID file not found, nothing to stop."
    exit 0
}

try {
    $data = Get-Content $pidFile -Raw | ConvertFrom-Json
    foreach ($procId in @($data.frontendPid, $data.gatewayPid)) {
        if ($procId) {
            Stop-Process -Id $procId -Force -ErrorAction SilentlyContinue
        }
    }
    Remove-Item $pidFile -Force -ErrorAction SilentlyContinue
    Write-Host "Port-forward stopped."
} catch {
    Write-Host "Stop failed, close port-forward terminals manually."
    throw
}
