# Can run from any directory; script resolves repo root.
# minikube: run `minikube docker-env | Invoke-Expression` first.
# kind: after build, run `kind load docker-image` for each image.
# Example:
#   .\deployments\k8s\scripts\build-images.ps1 -Tag "20260326-1"
#   .\deployments\k8s\scripts\build-images.ps1 -Tag "20260326-1" -AlsoLatest
param(
    [string]$Tag = "latest",
    [switch]$AlsoLatest
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSScriptRoot))
Set-Location $root

$services = @(
    @{ Image = "pim/gateway"; Path = "cmd/gateway" },
    @{ Image = "pim/auth-service"; Path = "cmd/auth-service" },
    @{ Image = "pim/user-service"; Path = "cmd/user-service" },
    @{ Image = "pim/friend-service"; Path = "cmd/friend-service" },
    @{ Image = "pim/conversation-service"; Path = "cmd/conversation-service" },
    @{ Image = "pim/group-service"; Path = "cmd/group-service" },
    @{ Image = "pim/file-service"; Path = "cmd/file-service" },
    @{ Image = "pim/log-service"; Path = "cmd/log-service" },
    @{ Image = "pim/frontend"; Path = "" }
)

foreach ($s in $services) {
    Write-Host "Building $($s.Image):$Tag ..."
    if ($s.Image -eq "pim/frontend") {
        docker build -f deployments/docker/Dockerfile.frontend -t "$($s.Image):$Tag" .
        if ($AlsoLatest) {
            docker tag "$($s.Image):$Tag" "$($s.Image):latest"
        }
    } else {
        docker build -f deployments/docker/Dockerfile --build-arg SERVICE_PATH=$($s.Path) -t "$($s.Image):$Tag" .
        if ($AlsoLatest) {
            docker tag "$($s.Image):$Tag" "$($s.Image):latest"
        }
    }
}

Write-Host "Done. Built tag: $Tag"
