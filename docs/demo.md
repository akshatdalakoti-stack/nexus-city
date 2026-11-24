# Nexus City: demo script

A guided walkthrough of about 15 minutes that shows every part of the system working on live
data. The figures in **Expect** come from measured runs (seed 42). The exact numbers change with
the time of day you reach, but the shape stays the same.

> Everything in Nexus City is synthetic. Say so at the start; the UI and the assistant say it
> too.

## Before you start (5 minutes before)

Start the stack in one of two ways.

**Kubernetes (Docker Desktop, the full platform):**

```powershell
powershell -File deploy\k8s\deploy.ps1
```

**Docker Compose:**

```powershell
docker compose up -d
```

Either way:

1. Open <http://localhost:3000>.
2. In the top bar, click **Restart** so the run begins at **Mon 05:30**.
3. Set the speed to **60×**. One simulated hour now passes per wall-clock minute.
4. Keep Grafana (<http://localhost:3001>, Kubernetes only) open in a second tab.

City Intelligence uses an LLM if `LLM_API_KEY` or `GEMINI_API_KEY` was set at deploy time.
Otherwise it uses rule-based mode, and the badge on its page tells you which.

## 1. The city wakes up: Overview (2 min)

*Talking point: a deterministic simulation drives everything; one clock is shared by every
service.*

- Watch the KPI tiles as the clock passes 07:00.
- **Expect:** between 07:00 and 09:00 the congestion index climbs from about 5% to 25–30%, and
  average speed drops from about 40 to about 15 km/h.
- Rule-based alerts open in **Active alerts**, for example "Heavy congestion on Highway A".
- The **Event feed** shows incidents and EMS calls.
- **Districts:** Southgate (D4) and Pinecrest (D12) have the slowest EMS response, about
  13–14 min. No hospital sits in the south-east.

## 2. Look closer: Live Twin (2 min)

*Talking point: the map is the same data, drawn as a twin.*

- Roads are coloured by congestion, and the vehicles move.
- Click a red road near the Highway A/B interchange. The inspector shows its speed, flow and
  history, plus the model's forecast.
- Turn on **Forecast +30 min** to recolour roads by predicted congestion.
- Switch to **Replay** and drag back an hour to see the rush build.

## 3. What happens next: AI Predictions and Model Lab (2 min)

*Talking point: real models, trained on* other *simulated cities and checked honestly.*

- **AI Predictions:**
  - roads to watch in the next 30 minutes, each linked to the map;
  - the 24-hour electricity outlook;
  - PM2.5 in an hour;
  - anomaly flags.
- **Live accuracy** compares each model against simple baselines as forecasts reach their target
  time. Traffic needs about 15 simulated minutes of data; energy needs an hour.
- **Model Lab:** test error against the baselines and against a city the models never saw.
  - The forecast models beat their baselines (PM2.5 by 35–49%).
  - The anomaly forest scores **below** simple rules on F1 for air and energy, and the page says
    so. That is why the rule alerts stay primary.

## 4. Test a decision: Scenario Lab (3 min)

*Talking point: branch the live city at this exact moment; only your change differs.*

- Build a **Road closure**, select Highway A (all 12 segments) for 2 hours, and click **Run
  experiment**.
- **Expect:** the result arrives in a few seconds.
  - Measured at 09:25 on a weekday: average journeys rose from 22.2 to 29.9 min, and 5,080
    vehicles were rerouted. At night the effect is much smaller.
  - The difference map shows nearby roads turning red as traffic diverts.
- Try **Signal timing → adaptive** during the rush. Expect journeys about 4% shorter.
- Run the same spec twice: the result hash is identical, because the engine is deterministic.

## 5. Ask the city: City Intelligence (3 min)

*Talking point: answers only from tool results, and every number is checked.*

Click the suggested questions:

1. "Summarize the city's current condition."
2. "Why is District 4 experiencing congestion?"
   - In the rush it names the worst roads and their speeds.
   - Off-peak it says D4 is *not* congested rather than inventing a cause. This was observed at
     06:03.
3. "Which district is likely to consume the most electricity tonight?"
4. "Which roads need attention in the next 30 minutes?"
5. "What happens if we close Highway A?" This runs a real scenario and takes 20–60 s. It also
   appears in the Scenario Lab.

Under each answer, open **tool calls** to see the raw data. The green line "N numbers checked
against tool data" is the grounding check.

## 6. Scale out: Infrastructure (2 min, Kubernetes only)

*Talking point: production-shaped: probes, limits, autoscaling and metrics, all real.*

- Pods are listed with status, restarts, and CPU and memory against their limits, read from the
  Kubernetes API.
- **Queue a burst** queues 12 real six-hour scenarios.
- **Expect** (measured):
  - workers go 0 → 1 within about 4 s and reach 3 by about 34 s;
  - the queue drains in about a minute;
  - workers scale back to 0 about a minute after the queue empties;
  - no scenario is lost, because a worker finishes its job before it exits.
- In Grafana, the **Nexus City** dashboard shows "Queue vs workers", scenario run time, gateway
  latency and per-service CPU and memory.

## Close

Points to leave with:

- All data is synthetic, and the system says so everywhere.
- Every number on screen traces back to a simulation step, a model output, a scenario run, or a
  live metric.
- Known limits are listed in [architecture.md](architecture.md#12-assumptions-and-limitations).
  For example, scenarios only work within the first 7 simulated days of a run, and model scores
  measure the simulator, not real-world skill.

## If something goes wrong

| Symptom | Fix |
|---|---|
| Top bar says **GATEWAY OFFLINE** | The gateway or Redis is down. On Kubernetes, run `kubectl -n nexus get pods`. In Compose, run `docker compose ps`. |
| Prediction chips grey or "not trained" | Models are missing. Run `cd ml; uv run nexus-ml train all`, then rebuild the images. |
| City Intelligence says "rule-based" | No LLM key was set. Set `LLM_API_KEY` or `GEMINI_API_KEY` and re-run `deploy.ps1`. |
| An assistant answer takes over a minute | The LLM provider is slow (free tier). The fallback model takes over after 45 s. |
| Infrastructure says "outside Kubernetes" | You are on Compose or local dev, which is expected. Use the Kubernetes deployment for this section. |
