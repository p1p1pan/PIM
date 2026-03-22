param(
  [Parameter(Mandatory = $true)]
  [string]$Token,
  [int]$Count = 80,
  [int]$SleepMs = 800,
  [int]$IntervalMs = 300,
  [string]$BaseUrl = "http://localhost:8080"
)

$headers = @{ Authorization = "Bearer $Token" }
$url = "$BaseUrl/api/v1/admin/observability/drill/latency?sleep_ms=$SleepMs"

Write-Host "Triggering synthetic latency drill..."
Write-Host "URL: $url"
Write-Host "Count=$Count IntervalMs=$IntervalMs"

for ($i = 1; $i -le $Count; $i++) {
  try {
    Invoke-WebRequest -UseBasicParsing -Method Get -Uri $url -Headers $headers -TimeoutSec 10 | Out-Null
  } catch {
    Write-Host "Request failed at $i: $($_.Exception.Message)"
  }
  if ($i % 20 -eq 0) {
    Write-Host "Sent $i / $Count"
  }
  Start-Sleep -Milliseconds $IntervalMs
}

Write-Host "Done. Check p95 latency panel and alert state."
