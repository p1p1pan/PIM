param(
  [Parameter(Mandatory = $true)]
  [string]$Token,
  [ValidateSet("http500", "latency")]
  [string]$Scenario = "http500",
  [string]$GatewayBaseUrl = "http://localhost:8080",
  [string]$PrometheusBaseUrl = "http://localhost:9090",
  [int]$TriggerCount = 80,
  [int]$TriggerIntervalMs = 400,
  [int]$LatencySleepMs = 800,
  [int]$WaitAlertSeconds = 90
)

$ErrorActionPreference = "Stop"

function Invoke-DrillTraffic {
  param(
    [string]$ScenarioName,
    [string]$BaseUrl,
    [string]$BearerToken,
    [int]$Count,
    [int]$IntervalMs,
    [int]$SleepMs
  )

  $headers = @{ Authorization = "Bearer $BearerToken" }
  if ($ScenarioName -eq "http500") {
    $url = "$BaseUrl/api/v1/admin/observability/drill/http-500"
    for ($i = 1; $i -le $Count; $i++) {
      try {
        Invoke-WebRequest -UseBasicParsing -Method Post -Uri $url -Headers $headers -TimeoutSec 5 | Out-Null
      } catch {
        # 预期会报 500，忽略异常继续打流量
      }
      Start-Sleep -Milliseconds $IntervalMs
    }
    return "PimDrillHttpErrorRate"
  }

  $url = "$BaseUrl/api/v1/admin/observability/drill/latency?sleep_ms=$SleepMs"
  for ($i = 1; $i -le $Count; $i++) {
    try {
      Invoke-WebRequest -UseBasicParsing -Method Get -Uri $url -Headers $headers -TimeoutSec 10 | Out-Null
    } catch {
      Write-Host ("latency drill request failed at {0}: {1}" -f $i, $_.Exception.Message)
    }
    Start-Sleep -Milliseconds $IntervalMs
  }
  return "PimDrillHttpLatencyP95"
}

function Get-FiringAlerts {
  param([string]$BaseUrl)
  $uri = "$BaseUrl/api/v1/alerts"
  $resp = Invoke-WebRequest -UseBasicParsing -Uri $uri -TimeoutSec 10
  $json = $resp.Content | ConvertFrom-Json
  if ($json.status -ne "success") {
    return @()
  }
  return @($json.data.alerts | Where-Object { $_.state -eq "firing" } | ForEach-Object { $_.labels.alertname })
}

Write-Host "== Alert Drill =="
Write-Host "Scenario: $Scenario"
Write-Host "Gateway:  $GatewayBaseUrl"
Write-Host "Prom:     $PrometheusBaseUrl"
Write-Host ""

$targetAlert = Invoke-DrillTraffic -ScenarioName $Scenario -BaseUrl $GatewayBaseUrl -BearerToken $Token -Count $TriggerCount -IntervalMs $TriggerIntervalMs -SleepMs $LatencySleepMs
Write-Host "Traffic finished. Target alert: $targetAlert"
Write-Host "Polling Prometheus alerts for up to $WaitAlertSeconds seconds..."

$deadline = (Get-Date).AddSeconds($WaitAlertSeconds)
$fired = $false
while ((Get-Date) -lt $deadline) {
  try {
    $alerts = Get-FiringAlerts -BaseUrl $PrometheusBaseUrl
    if ($alerts -contains $targetAlert) {
      $fired = $true
      break
    }
  } catch {
    Write-Host "prom query failed: $($_.Exception.Message)"
  }
  Start-Sleep -Seconds 5
}

if ($fired) {
  Write-Host ""
  Write-Host "[PASS] Alert fired: $targetAlert"
  exit 0
}

Write-Host ""
Write-Host "[FAIL] Alert did not fire in time: $targetAlert"
Write-Host "Tip: check Prometheus Targets, query windows, and drill intensity."
exit 1
