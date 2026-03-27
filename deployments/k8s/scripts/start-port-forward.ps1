param(
    [string]$Namespace = "pim",
    [int]$FrontendLocalPort = 8088,
    [int]$GatewayLocalPort = 8080
)

$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$pidFile = Join-Path $scriptDir ".port-forward.pids.json"

function Assert-KubectlReady {
    $null = Get-Command kubectl -ErrorAction Stop
    $null = kubectl get ns $Namespace 1>$null
}

function Stop-OldProcesses {
    if (-not (Test-Path $pidFile)) {
        return
    }
    try {
        $data = Get-Content $pidFile -Raw | ConvertFrom-Json
        foreach ($procId in @($data.frontendPid, $data.gatewayPid)) {
            if ($procId) {
                Stop-Process -Id $procId -Force -ErrorAction SilentlyContinue
            }
        }
    } catch {
        Write-Host "Old PID file parse failed, skip cleanup."
    }
    Remove-Item $pidFile -Force -ErrorAction SilentlyContinue
}

Assert-KubectlReady
Stop-OldProcesses

$frontendCmd = "kubectl port-forward -n $Namespace svc/frontend ${FrontendLocalPort}:8088"
$gatewayCmd = "kubectl port-forward -n $Namespace svc/gateway ${GatewayLocalPort}:8080"

$frontendProc = Start-Process powershell -ArgumentList @("-NoExit", "-Command", $frontendCmd) -PassThru
$gatewayProc = Start-Process powershell -ArgumentList @("-NoExit", "-Command", $gatewayCmd) -PassThru

[pscustomobject]@{
    namespace   = $Namespace
    frontendPid = $frontendProc.Id
    gatewayPid  = $gatewayProc.Id
    startedAt   = (Get-Date).ToString("s")
} | ConvertTo-Json | Set-Content -Path $pidFile -Encoding UTF8

Write-Host "Port-forward started:"
Write-Host "  Frontend: http://127.0.0.1:$FrontendLocalPort"
Write-Host "  Gateway : http://127.0.0.1:$GatewayLocalPort"
Write-Host "PID file: $pidFile"
