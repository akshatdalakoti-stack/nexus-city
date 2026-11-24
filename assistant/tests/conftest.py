"""A fake gateway serving real responses captured from a running stack
(tests/fixtures, written by tests/capture_fixtures.py), so tests are
deterministic and need no services."""

from __future__ import annotations

import json
from pathlib import Path

import httpx
import pytest

from nexus_assistant.gateway import Gateway

FIX = Path(__file__).parent / "fixtures"


def load(name: str):
    return json.loads((FIX / name).read_text(encoding="utf-8"))


def gateway_handler(requests: list | None = None):
    city, clock = load("city.json"), load("clock.json")
    latest, before = load("telemetry_latest.json"), load("telemetry_1h_ago.json")
    forecasts, anomalies, models = load("forecasts_latest.json"), load("anomalies_latest.json"), load("models.json")
    metas = {k: load(f"model_{k}.json") for k in ("traffic", "energy", "pm25", "anomaly")}
    scen_created, scen_done = load("scenario_created.json"), load("scenario_done.json")

    def handler(req: httpx.Request) -> httpx.Response:
        if requests is not None:
            requests.append(req)
        p = req.url.path
        routes = {
            "/api/city": city, "/api/clock": clock, "/api/telemetry/latest": latest,
            "/api/forecasts/latest": forecasts, "/api/anomalies/latest": anomalies, "/api/models": models,
        }
        if p in routes:
            return httpx.Response(200, json=routes[p])
        if p == "/api/telemetry":
            return httpx.Response(200, json=before)
        if p.startswith("/api/models/"):
            return httpx.Response(200, json=metas[p.rsplit("/", 1)[1]])
        if p == "/api/scenarios" and req.method == "POST":
            body = json.loads(req.content)
            if body["kind"] not in ("road_closure", "rush_hour", "signal_timing", "industrial_spike", "heatwave"):
                return httpx.Response(400, json={"error": "bad kind"})
            return httpx.Response(202, json=scen_created)
        if p.startswith("/api/scenarios/"):
            return httpx.Response(200, json=scen_done)
        return httpx.Response(404, json={"error": f"no route {p}"})

    return handler


@pytest.fixture
def requests_seen() -> list:
    return []


@pytest.fixture
async def gw(requests_seen):
    g = Gateway("http://gateway.test", client=httpx.AsyncClient(transport=httpx.MockTransport(gateway_handler(requests_seen))))
    yield g
    await g.close()


@pytest.fixture
def city():
    return load("city.json")
