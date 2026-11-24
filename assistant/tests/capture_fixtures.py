"""Capture real gateway responses as test fixtures.

    uv run python tests/capture_fixtures.py [http://localhost:8080]

Needs a running stack with the model services and a scenario worker. Run it
during a rush hour so the "why is it congested" fixtures are interesting.
"""

from __future__ import annotations

import json
import sys
import time
from datetime import datetime, timedelta
from pathlib import Path

import httpx

base = sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080"
out = Path(__file__).parent / "fixtures"
out.mkdir(exist_ok=True)
c = httpx.Client(base_url=base, timeout=120)


def save(name: str, data) -> None:
    (out / name).write_text(json.dumps(data, ensure_ascii=False), encoding="utf-8")
    print(f"  {name}: {len(json.dumps(data)) // 1024} KB")


# Pause so the snapshot is consistent.
c.post("/api/clock", json={"action": "pause"})
time.sleep(1.5)
try:
    save("city.json", c.get("/api/city").json())
    clock = c.get("/api/clock").json()
    save("clock.json", clock)
    latest = c.get("/api/telemetry/latest").json()
    save("telemetry_latest.json", latest)
    t = datetime.fromisoformat(latest["sim_time"].replace("Z", "+00:00")) - timedelta(hours=1)
    save("telemetry_1h_ago.json", c.get("/api/telemetry", params={"at": t.strftime("%Y-%m-%dT%H:%M:%SZ")}).json())
    save("forecasts_latest.json", c.get("/api/forecasts/latest").json())
    save("anomalies_latest.json", c.get("/api/anomalies/latest").json())
    save("models.json", c.get("/api/models").json())
    for k in ("traffic", "energy", "pm25", "anomaly"):
        save(f"model_{k}.json", c.get(f"/api/models/{k}").json())
    city = c.get("/api/city").json()
    hwy = [r["id"] for r in city["roads"] if r["name"] == "Highway A"]
    created = c.post("/api/scenarios", json={"kind": "road_closure", "params": {"road_ids": hwy}, "duration_min": 120}).json()
    save("scenario_created.json", created)
    for _ in range(240):
        sc = c.get(f"/api/scenarios/{created['id']}").json()
        if sc["status"] in ("done", "failed"):
            break
        time.sleep(0.5)
    save("scenario_done.json", sc)
finally:
    c.post("/api/clock", json={"action": "resume"})
