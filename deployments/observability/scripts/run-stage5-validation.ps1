param(
  [Parameter(Mandatory = $true)]
  [string]$Token,
  [string]$GatewayBaseUrl = "http://localhost:8080",
  [string]$PrometheusBaseUrl = "http://localhost:9090"
)

$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$drillScript = Join-Path $root "run-stage5-alert-drill.ps1"

if (-not (Test-Path $drillScript)) {
  Write-Host "[FAIL] Missing script: $drillScript"
  exit 1
}

Write-Host "== Alert Drill Validation Runner =="
Write-Host "Gateway:    $GatewayBaseUrl"
Write-Host "Prometheus: $PrometheusBaseUrl"
Write-Host ""

$results = @()

function Run-Scenario {
  param(
    [string]$Scenario
  )

  Write-Host "---- Running scenario: $Scenario ----"
  $ok = $false
  try {
    & $drillScript `
      -Token $Token `
      -Scenario $Scenario `
      -GatewayBaseUrl $GatewayBaseUrl `
      -PrometheusBaseUrl $PrometheusBaseUrl
    $ok = ($? -and $LASTEXITCODE -eq 0)
  } catch {
    Write-Host "[FAIL] $Scenario exception: $($_.Exception.Message)"
    $ok = $false
  }
  if ($ok) {
    Write-Host "[PASS] $Scenario"
  } else {
    Write-Host "[FAIL] $Scenario"
  }
  return [PSCustomObject]@{
    scenario = $Scenario
    passed   = $ok
  }
}

$results += Run-Scenario -Scenario "http500"
$results += Run-Scenario -Scenario "latency"

Write-Host ""
Write-Host "== Alert Drill Validation Summary =="
$passCount = 0
foreach ($r in $results) {
  $status = if ($r.passed) { "PASS" } else { "FAIL" }
  if ($r.passed) { $passCount++ }
  Write-Host "$($r.scenario): $status"
}

if ($passCount -eq $results.Count) {
  Write-Host "[PASS] All alert drill scenarios passed."
  exit 0
}

Write-Host "[FAIL] Some alert drill scenarios failed."
exit 1
