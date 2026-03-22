param(
  [Parameter(Mandatory = $true)]
  [string]$Token,
  [int]$Count = 120,
  [int]$IntervalMs = 500,
  [string]$BaseUrl = "http://localhost:8080"
)

$headers = @{ Authorization = "Bearer $Token" }
$url = "$BaseUrl/api/v1/admin/observability/drill/http-500"

Write-Host "Triggering synthetic 500 drill..."
Write-Host "URL: $url"
Write-Host "Count=$Count IntervalMs=$IntervalMs"

for ($i = 1; $i -le $Count; $i++) {
  try {
    Invoke-WebRequest -UseBasicParsing -Method Post -Uri $url -Headers $headers -TimeoutSec 5 | Out-Null
  } catch {
    # Expected: endpoint returns 500.
  }
  if ($i % 20 -eq 0) {
    Write-Host "Sent $i / $Count"
  }
  Start-Sleep -Milliseconds $IntervalMs
}

Write-Host "Done. Check Prometheus Alerts and Grafana SLO panel."
