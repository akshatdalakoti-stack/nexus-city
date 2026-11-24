# Builds every Nexus City image into the local Docker image store, which
# Docker Desktop's Kubernetes uses directly. Run from anywhere:
#   powershell -File deploy\k8s\build-images.ps1
# The ML image needs trained models in ml/models (see ml/README.md).
$ErrorActionPreference = "Continue"   # docker writes progress to stderr; exit codes are checked
$root = Resolve-Path (Join-Path $PSScriptRoot "..\..")

function Build([string]$tag, [string[]]$dockerArgs) {
    Write-Host "==> $tag" -ForegroundColor Cyan
    & docker build -t $tag @dockerArgs
    if ($LASTEXITCODE -ne 0) { throw "docker build failed for $tag" }
}

foreach ($svc in "sim", "gateway", "scenario", "recorder") {
    Build "nexus-city/${svc}:dev" @("--build-arg", "SERVICE=$svc", "$root\core")
}
Build "nexus-city/assistant:dev" @("$root\assistant")
# The browser calls the gateway on localhost:8080 (the LoadBalancer service).
Build "nexus-city/web:dev" @("--build-arg", "NEXT_PUBLIC_GATEWAY_URL=http://localhost:8080", "$root\web")
Build "nexus-city/ml:dev" @("$root\ml")

if (-not (Test-Path "$root\ml\models\traffic")) {
    throw "No trained models in ml\models. Train them first: cd ml; uv run nexus-ml generate; uv run nexus-ml train all"
}
# The Dockerfile is piped on stdin: BuildKit on Windows rejects a Dockerfile
# outside the build context ("invalid file request").
Write-Host "==> nexus-city/ml-models:dev" -ForegroundColor Cyan
Get-Content "$root\deploy\k8s\ml-models.Dockerfile" -Raw | docker build -t nexus-city/ml-models:dev -f - "$root\ml\models"
if ($LASTEXITCODE -ne 0) { throw "docker build failed for nexus-city/ml-models:dev" }

Write-Host "All images built." -ForegroundColor Green
