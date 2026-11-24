# Nexus City on Kubernetes

Plain manifests composed with Kustomize, which is built into `kubectl`, so you don't need Helm.
The target is **Docker Desktop's Kubernetes** (one node). The manifests are standard and also
work on kind or k3d once the images are loaded into the cluster.

```
deploy/k8s/
├── base/                 the application: namespace, config, Redis, PostGIS, sim, gateway (+ RBAC),
│                         scenario workers + KEDA ScaledObject, recorder, ML, assistant, web
├── monitoring/           Prometheus (pod discovery, namespace-scoped RBAC) and Grafana (+ dashboard)
├── overlays/docker-desktop/   local images (imagePullPolicy: Never), LoadBalancer services on localhost
├── ml-models.Dockerfile  inference image + trained artifacts
├── build-images.ps1      builds every image into the local Docker store
└── deploy.ps1            installs KEDA, creates the Secret, applies the overlay, waits for rollout
```

## Deploy

1. In Docker Desktop (4.99), open **Kubernetes** in the sidebar and click **Create cluster**.
   - **Cluster type:** pick **kubeadm** with one node. It uses Docker's own image store, so the
     locally built images are visible directly.
   - **kind** also works. `deploy.ps1` detects the kind node and copies the seven images into it
     (about 2 GB, a few minutes).
   - In this Docker Desktop version, the cluster's **Stop** button deletes the cluster. To free
     memory without losing it, quit Docker Desktop instead.
2. Stop anything already using ports 3000, 8080 or 3001, such as local dev servers or
   `docker compose`.
3. Build the images and deploy:

```powershell
powershell -File deploy\k8s\build-images.ps1
```

```powershell
powershell -File deploy\k8s\deploy.ps1
```

| URL | What |
|---|---|
| <http://localhost:3000> | Command center. **Infrastructure** shows the pods, KEDA and metrics. |
| <http://localhost:8080> | Gateway API (`/api/infra`, `/metrics`) |
| <http://localhost:3001> | Grafana, **Nexus City** dashboard (anonymous view) |

To remove everything except KEDA, run:

```powershell
powershell -File deploy\k8s\deploy.ps1 -Remove
```

## Secrets

The manifests contain no secrets. `deploy.ps1` creates the Secret `nexus-secrets` from your
`.env` or environment:

| Key | Source |
|---|---|
| `POSTGRES_PASSWORD` | `.env`. Failing that, the value already in the cluster. Failing that, a new random value. It may only use URL-safe characters. |
| `LLM_API_KEY` | `.env` `LLM_API_KEY` or `GEMINI_API_KEY`, or the same environment variables. Optional: without it the assistant uses rule-based mode. |
| `GRAFANA_ADMIN_PASSWORD` | Kept if it exists, otherwise a new random value. |

The values pass through a temporary file that is deleted at once; they never appear on a
command line or in the console. To read the Grafana admin password, run:

```powershell
kubectl -n nexus get secret nexus-secrets -o jsonpath="{.data.GRAFANA_ADMIN_PASSWORD}"
```

The output is base64-encoded.

## What runs

| Workload | Replicas | Requests → limits | Probes |
|---|---|---|---|
| `redis` | 1 | 50m/64Mi → 1 CPU/320Mi | `redis-cli ping` |
| `postgres` (StatefulSet, 2 Gi PVC) | 1 | 100m/128Mi → 1 CPU/512Mi | `pg_isready` |
| `sim` | exactly 1 (`Recreate`: it owns the clock) | 200m/64Mi → 1 CPU/256Mi | `/healthz` fails if the loop stalls |
| `gateway` | 1 (in-memory history) | 100m/96Mi → 1 CPU/256Mi | live `/api/health`, ready `/api/ready` |
| `scenario-worker` | **0–3, KEDA** | 250m/128Mi → 1 CPU/512Mi | `/healthz` |
| `recorder` | 1 | 50m/48Mi → 500m/256Mi | `/healthz` |
| `ml` (all four models) | 1 | 250m/384Mi → 1 CPU/1Gi | startup and live `/health`, ready `/ready` |
| `assistant` | 1 | 50m/96Mi → 500m/256Mi | `/health` |
| `web` | 1 | 50m/128Mi → 1 CPU/384Mi | `GET /` |
| `prometheus` | 1 | 100m/160Mi → 1 CPU/512Mi | `/-/ready`, `/-/healthy` |
| `grafana` | 1 | 50m/96Mi → 500m/256Mi | `/api/health` |

The limits add up to about 5 GiB, but the stack actually uses about 1.5–2 GiB, plus KEDA
(~150 MiB) and Kubernetes itself. The ML image with its models is 727 MB: it uses CPU-only xgboost
and leaves out training's pandas/pyarrow.

Every container has the same hardening:

- runs as non-root;
- runs without privilege escalation, with all capabilities dropped;
- uses the `RuntimeDefault` seccomp profile.

Go services, Redis, Prometheus, ML and the assistant also have read-only root filesystems. Only
the gateway and Prometheus get a Kubernetes API token; every other pod has
`automountServiceAccountToken: false`. The namespace enforces the `baseline` Pod Security
Standard and warns on `restricted`.

## Autoscaling scenario workers with KEDA

The `ScaledObject` in `base/scenario-worker.yaml` uses two `redis-streams` triggers on the
`nexus:scenarios` stream and the `scenario-workers` consumer group:

- **`lagCount: 1`** counts scenarios queued but not yet delivered to any worker, and asks for one
  worker per waiting scenario. Redis 7's consumer-group lag provides this count.
- **`pendingEntriesCount: 1`** counts scenarios a worker has received but not finished. It keeps
  busy workers from being scaled to zero.

Replicas range from 0 to 3. KEDA polls every 5 s and returns to zero 60 s after the queue empties.
Scale-down removes one pod per 30 s after a 60 s stabilisation window.

**Safe scale-down:**
- On SIGTERM, a worker finishes the scenario it is running. Its work context isn't cancelled by
  shutdown, and the pod grace period is 120 s.
- It then leaves the consumer group, so autoscaled pods don't build up as dead consumers.
- A worker that dies mid-scenario leaves the message pending. Another worker reclaims it after
  10 minutes.

To watch it scale, open **Infrastructure → Queue a burst**. This queues twelve real six-hour
heatwave scenarios, and you can see the replica slots, the queue and the "queue vs workers" chart
change. The results also appear in the Scenario Lab.

**Measured on Docker Desktop (kubeadm, one node):**

| Time after the burst | Workers | Queue |
|---|---|---|
| 0 s | 0 | 12 being queued |
| 4 s | **0 → 1** | 11 waiting, 1 running |
| 34 s | **1 → 3** | 4 waiting, 3 running |
| 63 s | 3 | drained |
| 93 s | **3 → 2** | scale-down, one pod per 30 s |
| 123 s | **→ 0** | 60 s cooldown after the queue emptied |

All 18 test scenarios (this run plus an earlier six) finished on four different worker pods, and
none failed during scale-down. Every stopped worker left the consumer group, which went back to 0
consumers.

A six-hour scenario takes about 5 s. A burst of six was drained by one worker before the
autoscaler's ~30 s reaction time had passed, so it never scaled beyond one worker. That is why the
button queues twelve.

## Monitoring

Every service exposes Prometheus metrics on `/metrics`:

- **Go services:** `nexus_sim_*`, `nexus_gateway_*`, `nexus_scenario_*` (including queue gauges
  read from Redis at scrape time), `nexus_recorder_*`, `nexus_stream_*`, plus Go runtime and
  process metrics.
- **ML service:** `nexus_ml_*`, for inference time, outputs published and models loaded.
- **Assistant:** `nexus_assistant_*`, for answers by mode, tool calls, ungrounded numbers and LLM
  failures.

Prometheus finds pods through `kubernetes_sd_configs` (role `pod`, own namespace) using the
`prometheus.io/scrape` and `prometheus.io/port` annotations. It labels each series with `pod` and
`component`, and keeps 12 h or at most 400 MB of history.

The **Grafana dashboard** (`monitoring/dashboards/nexus-city.json`, generated by
`make_dashboard.py`) covers:

- targets up;
- queue vs workers;
- scenario throughput and run time;
- simulation steps/s;
- stream throughput;
- gateway routes, latency and errors;
- inference time;
- assistant answers;
- CPU, memory and Go heap by component.

**The Infrastructure page** gets its data from the gateway:

- **`/api/infra`:** lists workloads, pods, HPAs and ScaledObjects through the Kubernetes API, using
  a read-only Role in the namespace. If metrics-server is installed it supplies per-pod CPU and
  memory; otherwise these come from the services' process metrics in Prometheus.
- **`/api/infra/series`:** runs a fixed set of PromQL queries (arbitrary queries are not
  accepted).

Pods that export no metrics (Redis, PostgreSQL, Grafana, web) show "not measured" rather than a
guess.

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `context "docker-desktop" does not exist` | No cluster. Create one on Docker Desktop's Kubernetes page. |
| Pods in `ErrImageNeverPull` | The images aren't in the cluster. Run `build-images.ps1`, then `deploy.ps1` (on kind it copies them in; `-SkipImageLoad` skips that). |
| Gateway or recorder restart once on a fresh cluster | They wait for PostgreSQL's first start (~30 s). Startup probes allow 3 minutes, so this should no longer happen. |
| Infrastructure page shows CPU "—" | Prometheus needs about two minutes of samples after it (re)starts. |
| Ports 3000/8080/3001 not answering | A local dev server or `docker compose` is already using them. Stop it, then re-run `deploy.ps1`. |

## Limitations

- **Single node:** Docker Desktop runs one node, so this shows scheduling and autoscaling, not
  high availability.
- **No persistence for Redis or Prometheus:** after a Redis restart, the simulation starts a new
  run.
- **One replica each:** the gateway keeps its live history in memory, so it runs one replica. The
  simulation must be a single replica because it owns the clock.
- **No ingress or TLS:** services are published on localhost through Docker Desktop's
  LoadBalancer support.
- **The web image has its gateway URL built in:** `http://localhost:8080`, set at build time.
- **Pod CPU and memory without metrics-server:** these then come from each service's own process
  metrics, so pods that export none aren't measured.
