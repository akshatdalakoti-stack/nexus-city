# Benchmarks

Every number here was measured on this project. Each section says **when**, **on what** and
**under which conditions**. Where a result is weak or loses to a simple baseline, it is shown as
it is.

> **Everything is synthetic.** The models are trained and tested on data from the project's own
> simulator. Their scores show how well they learned *the simulator*, not how they would do on a
> real city. Nothing here was compared against real-world data.

**Machine:** AMD Ryzen 7 7445HS (laptop), 15 GB RAM, Windows 11, Go 1.26.4, Python 3.12.
**Kubernetes figures:** Docker Desktop Kubernetes, one node, on the same laptop.

| Section | Measured |
|---|---|
| Simulation speed, tests | 2026-11-26 (re-run for this page) |
| ML models | Trained and evaluated 2026-11-19 (`ml/reports/`) |
| Scenarios, autoscaling, memory, images, frontend | 2026-11-21, during the Kubernetes and polish phases (not re-run since) |

## 1. Simulation speed

`go run ./cmd/headless -seed 42 -days N -quiet`, no output files, three runs each.

| Simulated time | Wall time (3 runs) | Simulated seconds per wall second | Final state hash |
|---|---|---|---|
| 48 h | 6.56 / 6.61 / 6.92 s | ~25,000–26,300 | `35a4e9e96b07` all 3 runs |
| 168 h (7 days) | 20.89 / 21.05 / 21.46 s | ~28,200–29,000 | `9e391841bfa9` all 3 runs |

- **Determinism:** every repeat ended in the same state hash.
- **Scenario cost:** a what-if scenario first re-simulates the run up to its branch point. That
  costs about **3 s per simulated day** before the scenario itself runs.

## 2. Tests

Run on 2026-11-26:

| Suite | Result |
|---|---|
| Go (`core`, `go test ./...`) | 50 passed, **5 skipped**, 0 failed |
| ML (`ml`, `pytest`) | 21 passed, 0 failed (90 s) |
| Assistant (`assistant`, `pytest`) | 25 passed, 0 failed (16 s) |
| Web (`npm run lint`, `npm run typecheck`) | No errors |

- **Skipped tests:** the 5 skipped Go tests need a PostGIS database and only run when
  `NEXUS_TEST_DATABASE_URL` is set. They are the database tests and the full scenario integration
  test. They were not run for this page.
- **No CI:** nothing runs the tests automatically.
- **No web tests:** the web app has no unit or end-to-end tests.

## 3. Machine learning

### Setup

- **Data:** trained on simulator seeds 101–103.
  - Days 0–20 are for training and days 20–24 for validation (early stopping).
  - The **test** set is days 24–28.
  - The **holdout** set is a city layout (seed 104) the models never saw.
- **No leakage:**
  - rows whose target falls into a later split are dropped (an embargo);
  - the live city (seed 42) is never used for training;
  - tests check that features never see the future, and that training and live inference build
    the same features.
- **Metric:** MAE is the mean absolute error. Lower is better.

### Traffic: road congestion index (0 = free flow, 1 = jammed)

| Horizon | XGBoost | Ridge (best baseline) | Persistence | Same time yesterday | Gain vs best |
|---|---|---|---|---|---|
| 15 min, test | 0.043 | 0.049 | 0.053 | 0.066 | 10.9% |
| 30 min, test | 0.045 | 0.051 | 0.057 | 0.066 | 12.0% |
| 60 min, test | 0.046 | 0.054 | 0.062 | 0.066 | 15.8% |
| 15 / 30 / 60 min, unseen city | 0.050 / 0.053 / 0.054 | 0.056 / 0.059 / 0.063 | — | — | 9.6 / 10.8 / 15.1% |

**Read honestly:**
- XGBoost beats a plain linear model (ridge) by only **10–16%**.
- In weekday rush hours, the error is about **3× higher** (MAE 0.14–0.15).
- The error barely grows from 15 to 60 minutes. Single-road congestion is noisy because each agent
  stands for 8 vehicles, and that noise floor dominates.

### Energy: district demand in MW, 1–24 h ahead

| Horizon | XGBoost | 7-day seasonal naive (best baseline) | 24 h seasonal naive | Gain vs best |
|---|---|---|---|---|
| 1 h, test | 1.26 | 2.67 | 3.24 | 52.9% |
| 12 h, test | 1.48 | 2.67 | 3.29 | 44.5% |
| 24 h, test | 1.70 | 2.73 | 3.50 | 37.9% |
| 1 h / 24 h, unseen city | 1.35 / 1.61 | 2.76 | 2.98 | 51.0 / 41.7% |

**Read honestly:**
- The simulator's demand is a smooth daily profile plus noise, so this task is easier than real
  grid forecasting.
- The model sees no weather **forecast**, only current and past temperature.

### PM2.5: station reading (synthetic µg/m³), 30 and 60 min ahead

| Horizon | XGBoost | Persistence (only baseline) | Gain |
|---|---|---|---|
| 30 min, test / unseen city | 1.08 / 1.41 | 1.66 / 2.04 | 34.6 / 31.0% |
| 60 min, test / unseen city | 1.31 / 1.86 | 2.58 / 3.30 | 49.4 / 43.7% |

**Read honestly:**
- The only baseline is persistence (the last reading carried forward), which is weak.
- The error rises about **30–42%** on the unseen city.
- The pollution model is a toy dispersion model, **not** an air-quality model.

### Anomaly detection: Isolation Forest vs simple rules

F1 combines precision and recall; higher is better. Labels are the anomalies the simulator injects
on purpose.

| Domain | Split | Isolation Forest F1 | Rule baseline F1 | Winner |
|---|---|---|---|---|
| Traffic (roads) | test | 0.123 | 0.152 | **Rules** |
| Traffic (roads) | unseen city | 0.140 | 0.140 | Tie |
| Energy (districts) | test | 0.291 | 0.766 | **Rules, by far** |
| Energy (districts) | unseen city | 0.314 | 0.312 | Tie |
| Air (stations) | test | 0.475 | 0.718 | **Rules** |
| Air (stations) | unseen city | 0.444 | 0.646 | **Rules** |

**Read honestly:**
- The learned detector **never clearly beats simple rules.** That is why the rule alerts stay
  primary and the forest is shown only as a second opinion.
- Traffic precision is **7–9%** for both methods, so more than 9 in 10 traffic flags are false
  alarms.

## 4. System

Measured on 2026-11-21 on Docker Desktop Kubernetes (one node). Not re-run since.

| What | Result | Conditions |
|---|---|---|
| One 2-hour what-if scenario | 1.5–3 s, including model forecasts | Branching at 08:00 on day 1. Later branch points add ~3 s per simulated day of replay. |
| Autoscaling, 12-scenario burst | 0 → 1 worker at 4 s, 3 workers at 34 s, queue empty at 63 s, back to 0 at ~123 s | KEDA, 0–3 workers. 18 test scenarios completed on 4 different pods, none failed. |
| Idle memory per service | ML (4 models) ~380 MiB, assistant ~70 MiB, gateway ~25 MiB, recorder ~19 MiB, sim ~17 MiB | Measured at idle; under load is higher |
| Container images | Go services ~22–28 MB each, assistant ~245 MB, web ~313 MB, ML serving image 623 MB (was 1.51 GB) | The ML image with all four models baked in is 727 MB |
| Gateway data endpoints | < 10 ms at p95 | Local, single user |
| Overview page main thread | Blocked ~3.5% of the time (was ~20%) at 60× speed | Chrome long-task measurement over 15 s; other pages had 0 long tasks |

## 5. What these numbers do not show

- **Real-world accuracy.** No model was tested on real data.
- **Load.** There was no load test with many concurrent users or WebSocket clients. Latency figures
  are for a single local user.
- **Availability.** Everything ran on a single machine; there is no measure of uptime or failover.
- **LLM quality.** City Intelligence answers took 10–30 s in testing, and about a minute when they
  ran a scenario. There is no benchmark of answer correctness. A grounding check flags numbers that
  don't appear in the data, but it can't catch wrong reasoning about correct numbers.
