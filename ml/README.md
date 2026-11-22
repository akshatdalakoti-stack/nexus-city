# nexus-ml — Nexus City machine learning

Dataset generation, training, evaluation and live inference for the Nexus City
digital twin. Everything here is trained and evaluated on **synthetic**
simulation data. Scores measure how well a model learned the simulator, not
real-world forecasting skill.

## Quick start

Requires [uv](https://docs.astral.sh/uv/) and the Go toolchain (the
simulation generates the data).

```powershell
cd ml
uv sync --extra tracking
uv run nexus-ml generate      # 3 training cities x 28 days + 1 held-out city x 14 days (~2 min)
uv run nexus-ml train all     # traffic, energy, pm25, anomaly (~6 min on a laptop CPU)
uv run nexus-ml report        # print the benchmark of the current models
uv run pytest                 # 20 tests
```

To serve the models against the running simulation (all four models in one
process; Compose runs one per container):

```powershell
$env:REDIS_URL = "redis://localhost:6379/0"
uv run nexus-ml serve --models traffic,energy,pm25,anomaly --port 8090
```

Experiment history is in `ml/mlruns`. To browse it, run
`uv run --with mlflow mlflow ui --backend-store-uri ml/mlruns`.

## Layout

| Module | Role |
|---|---|
| `config.py` | Paths, simulated time, which seeds train / hold out, chronological split, horizons |
| `citymeta.py` | Static per-entity attributes and road adjacency from a city JSON |
| `grid.py` | Dense `[time × entity]` arrays on the 5-minute grid, from datasets **or** live telemetry |
| `features.py` | All forecasting features; identical code for training and serving |
| `forecast.py` | Specs, training, evaluation, `ForecastModel` inference for traffic / energy / PM2.5 |
| `anomaly.py` | Isolation Forest detectors, labels from simulation events, rule baselines, `AnomalyModel` |
| `registry.py` | Versioned artifacts: `models/<kind>/<version>/` plus a `CURRENT` pointer |
| `service.py` | FastAPI inference service; publishes `nexus.forecast/v1` and `nexus.anomaly/v1` |
| `training.py`, `reports.py`, `cli.py` | Orchestration, Markdown reports, command line |

## How the evaluation stays honest

- **The live city is never trained on.** The simulation is deterministic, so
  seed 42 (the live twin) would leak its own future into training. Training
  uses seeds 101–103.
- **Chronological splits.** Days 0–19 train, 20–23 validation (early
  stopping, threshold tuning), 24–27 test. Rows whose target crosses into a
  later split are dropped (embargo).
- **A city the models never saw.** Seed 104 has a different layout,
  population and signal timing, and is used only for evaluation.
- **No look-ahead.** `test_features_do_not_see_the_future` scrambles all data
  after time *t* and checks that every feature at *t* is unchanged.
- **Train/serve parity.** `test_live_telemetry_builds_the_same_grid_as_the_dataset`
  checks that the live telemetry path produces exactly the arrays the model
  was trained on.
- **Baselines.** Each model is compared with naive baselines (persistence,
  same time yesterday, seasonal naive) and, for traffic, a ridge regression.
  The anomaly detectors are compared with rule baselines tuned the same way.
- **Live scoring.** The gateway scores every live forecast against what the
  simulation then did, next to the baseline (AI Predictions page).

Missing data: offline sensors are `NaN`, and stuck sensors are excluded as
inputs and targets. XGBoost handles `NaN` natively. Isolation Forest inputs
are imputed with training medians, plus a missing-reading indicator.

## Results (current versions)

See `reports/*.md` for full tables and the Model Lab page in the app.
In short:

| Model | Test MAE vs best baseline | Unseen city |
|---|---|---|
| Traffic congestion +15 / +30 / +60 min | −11% / −12% / −16% vs ridge (−19% / −21% / −26% vs persistence) | −10% / −11% / −15% vs ridge |
| District energy +1 h / +6 h / +24 h | −53% / −51% / −38% vs 7-day seasonal naive | −51% / −50% / −42% |
| Station PM2.5 +30 / +60 min | −35% / −49% vs persistence | −31% / −44% |

Isolation Forest does **not** beat the simple rules overall. On test days,
F1 is 0.12 vs 0.15 for traffic incidents, 0.29 vs 0.77 for energy surges, and
0.48 vs 0.72 for air-quality faults. On the unseen city it ties for traffic
and energy. It catches every energy surge episode and has the best
air-quality precision. The app therefore keeps the rule-based alerts primary
and shows the forest as a second opinion.
