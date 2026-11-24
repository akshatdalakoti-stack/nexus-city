# Nexus City — AI-Powered Smart City Digital Twin

A live digital twin of **Nexus City**, a fictional city of ~360,000 people.

- A deterministic **Go simulation** runs the city's traffic, signals, incidents, weather, air quality,
  electricity demand and emergency calls.
- **XGBoost and Isolation Forest** models, trained on *other* simulated cities, forecast and watch it
  live.
- A **what-if engine** branches the live city to test decisions such as road closures, adaptive
  signals or a heatwave.
- **City Intelligence** answers questions from live data, forecasts and scenario runs, and checks
  every number against its source.
- A **Next.js command center** shows it all in real time.
- The whole system runs on **Kubernetes**: KEDA scales the scenario workers on the queue, and
  Prometheus and Grafana monitor every service.

![go](https://img.shields.io/badge/Go-1.26-00ADD8) ![python](https://img.shields.io/badge/Python-3.12-3776AB) ![next](https://img.shields.io/badge/Next.js-16-black) ![k8s](https://img.shields.io/badge/Kubernetes-KEDA-326CE5)

> **Everything here is synthetic.** Every value comes from the simulation, and nothing is a
> measurement of a real city. Model scores show how well a model learned the simulator, not
> real-world forecasting skill.

## Features

| Page | What it does |
|---|---|
| **Overview** | City-wide congestion, grid load, PM2.5, live alerts and events |
| **Live Twin** | MapLibre map with moving vehicles, layers, an entity inspector, replay of past hours and a +30 min forecast overlay |
| **AI Predictions** | Roads to watch, the 24 h electricity outlook, air quality in an hour, anomaly flags and live accuracy against baselines |
| **Model Lab** | The benchmark for each served model: test days vs an unseen city, hard cases, feature importance |
| **Scenario Lab** | Close roads, add demand, try adaptive signals, an industrial spike or a heatwave, then compare baseline and scenario side by side |
| **City Intelligence** | A chat assistant with nine data tools, either an OpenAI-compatible LLM or a rule-based mode, plus a grounding check on every number |
| **Infrastructure** | Live pods, workloads and KEDA autoscaling read from the Kubernetes API and Prometheus |

## Architecture

```mermaid
flowchart LR
  UI["Next.js command center"] <-->|REST + WebSocket| GW["API gateway (Go)"]
  SIM["Simulation worker (Go)"] -->|XADD| R[("Redis Streams")]
  R -->|XREAD| GW
  R --> ML["Forecast + anomaly services (Python)"]
  ML -->|forecasts| R
  GW -->|scenario queue| R
  R -->|consumer group| SW["Scenario workers (Go, 0–3 via KEDA)"]
  SW --> PG[("PostgreSQL + PostGIS")]
  R --> REC["Recorder (Go)"] --> PG
  GW <--> AS["City Intelligence (Python)"]
  AS -.-> LLM["OpenAI-compatible LLM (optional)"]
```

- **One clock, one schema:** the simulation owns the clock, and every message shares one envelope
  (`schema`, `run_id`, `seed`, `tick`, `sim_time`, `payload`).
- **Deterministic branching:** the state after *N* steps depends only on `(seed, N)`. A scenario
  replays the run up to a moment, then clones it into a baseline branch and a scenario branch.
  The same spec always gives the same result hash.
- **Async scenario execution:** the gateway queues an ID and returns at once. Workers in a Redis
  consumer group pick up scenarios, with at-least-once delivery and reclaim of stalled jobs.

The full design, event schemas, database schema and simulation model are in
[docs/architecture.md](docs/architecture.md).

## Tech stack

| Layer | Tools |
|---|---|
| Simulation, gateway, workers | Go 1.26, Redis Streams, WebSockets |
| Machine learning | Python 3.12, XGBoost, scikit-learn, FastAPI, MLflow, uv |
| Assistant | Python, FastAPI, OpenAI SDK (any OpenAI-compatible endpoint; Gemini by default) |
| Storage | PostgreSQL 17 + PostGIS 3.5 |
| Frontend | Next.js 16, Tailwind 4, shadcn/ui, MapLibre GL, Recharts, Motion |
| Platform | Docker, Kubernetes (Kustomize), KEDA, Prometheus, Grafana |

## Quick start

Copy `.env.example` to `.env` and set `POSTGRES_PASSWORD`:

```powershell
Copy-Item .env.example .env
```

Train the models once (about 8 minutes, needs Go and [uv](https://docs.astral.sh/uv/)). Without
them everything else still runs, and the prediction panels say that no model is available.

```powershell
cd ml
uv sync --extra tracking
uv run nexus-ml generate
uv run nexus-ml train all
```

### Option A: Docker Compose

```powershell
docker compose up --build
```

Open <http://localhost:3000>. The gateway is on <http://localhost:8080>. City Intelligence runs in
rule-based mode unless `LLM_API_KEY` (or `GEMINI_API_KEY`) is set. To add scenario workers:
`docker compose up -d --scale scenario-worker=3`.

### Option B: Kubernetes (Docker Desktop)

Create a cluster on Docker Desktop's Kubernetes page (kubeadm, 1 node), then:

```powershell
powershell -File deploy\k8s\build-images.ps1
powershell -File deploy\k8s\deploy.ps1
```

| Service | URL |
|---|---|
| Command center | <http://localhost:3000> |
| Gateway | <http://localhost:8080> |
| Grafana | <http://localhost:3001> |

Details are in [deploy/k8s/README.md](deploy/k8s/README.md).

### Option C: Local development

You need Go 1.26+, Node 24+, a Redis 7 compatible server on `localhost:6379`, and, for scenarios,
a PostGIS database. Run each service in its own terminal:

```powershell
cd core; go run ./cmd/sim          # simulation worker
cd core; go run ./cmd/gateway      # REST + WebSocket API
cd core; go run ./cmd/scenario     # scenario worker
cd core; go run ./cmd/recorder     # stream → database writer
cd ml; uv run nexus-ml serve --models traffic,energy,pm25,anomaly
cd assistant; uv run nexus-assistant serve
cd web; npm install; npm run dev
```

Set `REDIS_URL`, `DATABASE_URL` and the other variables from [`.env.example`](.env.example). The
simulation starts at **Mon 1 June 2026, 05:30**. Switch to **60×** in the top bar to watch the
morning rush build.

A 15-minute guided tour is in [docs/demo.md](docs/demo.md).

## Project structure

```
core/        Go module: simulation engine, city generator, gateway, scenario worker, recorder
  cmd/       sim · gateway · scenario · recorder · headless (offline batch runner)
  internal/  sim, city, scenario, gateway, bus, events, store (PostGIS + migrations), ...
ml/          Python package nexus_ml: datasets, features, forecasting, anomaly detection, FastAPI
assistant/   Python package nexus_assistant: tools, LLM loop, rule-based mode, grounding check
web/         Next.js command center
deploy/k8s/  Kustomize manifests, KEDA ScaledObject, Prometheus + Grafana, build/deploy scripts
docs/        Architecture and demo script
```

## Machine learning

Models train only on cities other than the live one, use embargoed chronological splits and are
also tested on a held-out city. Tests guard against look-ahead leakage and train/serve skew.

| Model | Target | Test MAE vs best baseline |
|---|---|---|
| Traffic (XGBoost) | Road congestion at +15/30/60 min | −11 / −12 / −16% vs ridge |
| Energy (XGBoost) | District demand at +1…24 h | −53% (1 h) … −38% (24 h) vs seasonal naive |
| PM2.5 (XGBoost) | Station PM2.5 at +30/60 min | −35 / −49% vs persistence |
| Anomaly (Isolation Forest) | Incidents, surges, faulty sensors, emission releases | Shown as a second opinion; rules stay primary |

More in [ml/README.md](ml/README.md) and `ml/reports/`.

## Tests

```powershell
cd core; go test ./...
cd ml; uv run pytest
cd assistant; uv run pytest
cd web; npm run lint; npm run typecheck; npm run build
```

The tests cover:

- simulation determinism, and that clock speed never changes results;
- road-graph connectivity and schema compatibility between Go, Python and TypeScript;
- WebSocket delivery and an end-to-end flow from the worker through Redis and the gateway to REST;
- data leakage and train/serve parity for the models;
- scenario reproducibility, plus a complete scenario run through queue → worker → PostGIS → model
  service.

Go tests use an in-memory Redis (miniredis). The database tests run when `NEXUS_TEST_DATABASE_URL`
points at a scratch PostGIS database, and are skipped otherwise.

## API

The gateway (port 8080) serves the whole system:

- **City and clock:** `/api/city`, `/api/clock`
- **Telemetry:** `/api/telemetry/*`, `/api/history/{kind}/{id}`, `/api/events`
- **Models:** `/api/models`, `/api/forecasts/*`, `/api/anomalies/latest`
- **Scenarios:** `/api/scenarios`
- **Assistant:** `/api/assistant/chat`
- **Infrastructure:** `/api/infra`
- **Live stream:** `/ws`
- **Metrics:** `/metrics` (every service has one)

The route table is in [core/internal/gateway/server.go](core/internal/gateway/server.go).

## Configuration

See [`.env.example`](.env.example). The only secrets are `POSTGRES_PASSWORD` and the optional
`LLM_API_KEY` (or `GEMINI_API_KEY`). They live in `.env`, which git ignores, and are never
written into the code or manifests.

## Limitations

- The simulation models are simple and synthetic.
- ML scores measure how well the models learned the simulator, not real-world skill.
- Scenario outcomes are the model's response to a change, not real-world evidence.
- Scenarios work within the first 7 simulated days of a run.
- City Intelligence reports what the data shows, not why.

The full list is in
[docs/architecture.md § 12](docs/architecture.md#12-assumptions-and-limitations).
