param(
    [Parameter(Mandatory = $true)]
    [string]$Tag,
    [string]$Namespace = "pim",
    [switch]$Wait
)

$ErrorActionPreference = "Stop"

$deployments = @(
    @{ Kind = "statefulset"; Name = "gateway"; Container = "gateway"; Image = "pim/gateway" },
    @{ Kind = "deploy"; Name = "auth-service"; Container = "auth-service"; Image = "pim/auth-service" },
    @{ Kind = "deploy"; Name = "user-service"; Container = "user-service"; Image = "pim/user-service" },
    @{ Kind = "deploy"; Name = "friend-service"; Container = "friend-service"; Image = "pim/friend-service" },
    @{ Kind = "deploy"; Name = "conversation-service"; Container = "conversation-service"; Image = "pim/conversation-service" },
    @{ Kind = "deploy"; Name = "group-service"; Container = "group-service"; Image = "pim/group-service" },
    @{ Kind = "deploy"; Name = "file-service"; Container = "file-service"; Image = "pim/file-service" },
    @{ Kind = "deploy"; Name = "log-service"; Container = "log-service"; Image = "pim/log-service" },
    @{ Kind = "deploy"; Name = "frontend"; Container = "frontend"; Image = "pim/frontend" }
)

foreach ($d in $deployments) {
    $full = "$($d.Image):$Tag"
    $kind = if ($d.Kind) { $d.Kind } else { "deploy" }
    Write-Host "Setting $kind/$($d.Name) $($d.Container) -> $full"
    kubectl set image "$kind/$($d.Name)" "$($d.Container)=$full" -n $Namespace | Out-Null
}

if ($Wait) {
    foreach ($d in $deployments) {
        $kind = if ($d.Kind) { $d.Kind } else { "deploy" }
        Write-Host "Waiting rollout: $kind/$($d.Name)"
        kubectl rollout status "$kind/$($d.Name)" -n $Namespace
    }
}

Write-Host "Done. All workloads switched to tag: $Tag"
