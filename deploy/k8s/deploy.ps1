# Deploys Nexus City to Docker Desktop's Kubernetes:
#   powershell -File deploy\k8s\deploy.ps1            # install or update
#   powershell -File deploy\k8s\deploy.ps1 -Remove    # delete the namespace (keeps KEDA)
#   -SkipImageLoad   don't copy images into a kind node (they are already there)
#
# Secrets never appear in the manifests. They are read from .env or the
# environment and stored only in the cluster Secret `nexus-secrets`:
#   POSTGRES_PASSWORD       from .env, else the existing Secret, else a new random value
#   LLM_API_KEY             from .env or the LLM_API_KEY / GEMINI_API_KEY environment variables (optional)
#   GRAFANA_ADMIN_PASSWORD  kept if it exists, else a new random value
param(
    [switch]$Remove,
    [switch]$SkipImageLoad,
    [string]$Context = "docker-desktop",
    [string]$KedaVersion = "2.17.2"
)
# "Continue", not "Stop": kubectl prints harmless warnings on stderr, which
# Windows PowerShell 5.1 would turn into terminating errors. Every native
# call checks its exit code instead.
$ErrorActionPreference = "Continue"
$root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
$ns = "nexus"

function Kube {
    & kubectl --context $Context @args 2>&1 | ForEach-Object { "$_" }
    if ($LASTEXITCODE -ne 0) { throw "kubectl $args failed" }
}
# Probes whose failure is an answer, not an error.
function KubeQuiet { & kubectl --context $Context @args *> $null; return $LASTEXITCODE -eq 0 }

if (-not (KubeQuiet get nodes)) {
    throw "Kubernetes context '$Context' is not reachable. Enable Kubernetes in Docker Desktop (Settings > Kubernetes)."
}

if ($Remove) {
    Kube delete namespace $ns --ignore-not-found
    Write-Host "Removed namespace $ns. KEDA is still installed (namespace keda)." -ForegroundColor Yellow
    return
}

# ---- Images: Docker Desktop's kind clusters have their own containerd image
# store, so copy the locally built images into the node. (kubeadm clusters
# share Docker's store and need nothing.) cmd.exe does the piping because
# Windows PowerShell 5.1 corrupts binary data piped between programs.
$kindNode = (& docker ps --filter "name=^desktop-control-plane$" --format "{{.Names}}")
if (-not $kindNode) {
    & docker exec desktop-control-plane true *> $null
    if ($LASTEXITCODE -eq 0) { $kindNode = "desktop-control-plane" }
}
if ($kindNode -and -not $SkipImageLoad) {
    foreach ($img in "sim", "gateway", "scenario", "recorder", "assistant", "web", "ml-models") {
        Write-Host "==> loading nexus-city/${img}:dev into the kind node" -ForegroundColor Cyan
        & cmd /c "docker save nexus-city/${img}:dev | docker exec -i $kindNode ctr -n k8s.io images import --digests - >NUL"
        if ($LASTEXITCODE -ne 0) { throw "could not load nexus-city/${img}:dev (build it with build-images.ps1)" }
    }
}

# ---- KEDA (core install: operator + metrics server, no admission webhooks)
if (-not (KubeQuiet get crd scaledobjects.keda.sh)) {
    Write-Host "==> Installing KEDA $KedaVersion" -ForegroundColor Cyan
    Kube apply --server-side -f "https://github.com/kedacore/keda/releases/download/v$KedaVersion/keda-$KedaVersion-core.yaml"
}
Kube -n keda rollout status deployment/keda-operator --timeout=180s
Kube -n keda rollout status deployment/keda-metrics-apiserver --timeout=180s

# ---- Secrets
function Read-DotEnv([string]$path) {
    $vals = @{}
    if (Test-Path $path) {
        foreach ($line in Get-Content $path) {
            if ($line -match '^\s*([A-Z0-9_]+)\s*=\s*([^#]*?)\s*(#.*)?$') { $vals[$Matches[1]] = $Matches[2].Trim('"', "'") }
        }
    }
    return $vals
}
function New-Secret {
    $bytes = New-Object byte[] 18
    [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    return ([Convert]::ToBase64String($bytes) -replace '[+/=]', 'x')   # URL-safe
}
function Get-SecretKey([string]$key) {
    $v = & kubectl --context $Context -n $ns get secret nexus-secrets -o "jsonpath={.data.$key}" 2>$null
    if ($LASTEXITCODE -ne 0 -or -not $v) { return $null }
    return [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($v))
}

$dotenv = Read-DotEnv "$root\.env"
& kubectl --context $Context create namespace $ns --dry-run=client -o yaml | kubectl --context $Context apply -f - | Out-Null

$pg = $dotenv["POSTGRES_PASSWORD"]
if (-not $pg -or $pg -eq "change-me") { $pg = Get-SecretKey "POSTGRES_PASSWORD" }
if (-not $pg) { $pg = New-Secret }
if ($pg -notmatch '^[A-Za-z0-9._~-]+$') { throw "POSTGRES_PASSWORD may only contain letters, digits and . _ ~ - (it is used in a URL)." }
$llm = $dotenv["LLM_API_KEY"]
if (-not $llm) { $llm = $dotenv["GEMINI_API_KEY"] }
if (-not $llm) { $llm = $env:LLM_API_KEY }
if (-not $llm) { $llm = $env:GEMINI_API_KEY }
$graf = Get-SecretKey "GRAFANA_ADMIN_PASSWORD"
if (-not $graf) { $graf = New-Secret }

# Written to a private temp file (not the command line) and deleted at once.
$tmp = New-TemporaryFile
try {
    $lines = @("POSTGRES_PASSWORD=$pg", "GRAFANA_ADMIN_PASSWORD=$graf")
    if ($llm) { $lines += "LLM_API_KEY=$llm" }
    Set-Content -Path $tmp -Value $lines -Encoding ascii
    & kubectl --context $Context -n $ns create secret generic nexus-secrets --from-env-file=$tmp --dry-run=client -o yaml |
        kubectl --context $Context apply -f - | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "could not create the nexus-secrets Secret" }
} finally {
    Remove-Item $tmp -Force
}
$mode = if ($llm) { "LLM mode" } else { "rule-based mode (no LLM key found)" }
Write-Host "Secret nexus-secrets is up to date; City Intelligence will run in $mode." -ForegroundColor Cyan

# ---- Application and monitoring
Write-Host "==> Applying deploy/k8s/overlays/docker-desktop" -ForegroundColor Cyan
Kube apply -k "$root\deploy\k8s\overlays\docker-desktop"
# Restart workloads whose config or secret may have changed without a spec change.
Kube -n $ns rollout restart deployment/gateway deployment/assistant | Out-Null

foreach ($w in "statefulset/postgres", "deployment/redis", "deployment/sim", "deployment/gateway", "deployment/recorder",
               "deployment/ml", "deployment/assistant", "deployment/web", "deployment/prometheus", "deployment/grafana") {
    Kube -n $ns rollout status $w --timeout=300s
}

Write-Host ""
Write-Host "Nexus City is running on Kubernetes:" -ForegroundColor Green
Write-Host "  Command center  http://localhost:3000   (Infrastructure page: /infrastructure)"
Write-Host "  Gateway API     http://localhost:8080"
Write-Host "  Grafana         http://localhost:3001   (anonymous view; admin password: kubectl -n nexus get secret nexus-secrets -o jsonpath='{.data.GRAFANA_ADMIN_PASSWORD}')"
Write-Host "Scenario workers scale from 0 to 3 with the queue (KEDA)."
