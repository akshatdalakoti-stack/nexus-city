"""Model inference and the service's HTTP interface. These use the trained
models in ml/models (run `nexus-ml train all` first); they are skipped if a
model has not been trained."""

from __future__ import annotations

import json

import numpy as np
import pytest

from nexus_ml import registry
from nexus_ml.anomaly import AnomalyModel
from nexus_ml.citymeta import CityMeta
from nexus_ml.forecast import SPECS, ForecastModel
from nexus_ml.grid import Grid


def _need(kind: str, models_dir):
    if not registry.current_version(kind, models_dir):
        pytest.skip(f"{kind} model not trained")


@pytest.mark.parametrize("kind", ["traffic", "energy", "pm25"])
def test_forecast_inference_shape_range_and_version(small_run, models_dir, kind):
    _need(kind, models_dir)
    model = ForecastModel.load(kind, models_dir)
    meta = CityMeta.from_city(small_run["city"])
    g = Grid.from_telemetry(small_run["telemetry"][-model.history_steps :], meta)
    out = model.predict_last(g)
    n = {"road": meta.n_roads, "district": meta.n_districts, "station": meta.n_stations}[SPECS[kind].entity]
    assert list(out) == list(SPECS[kind].horizons)
    lo, hi = SPECS[kind].clip
    for h, v in out.items():
        assert v.shape == (n,) and np.isfinite(v).all(), h
        assert (v >= lo).all() and (hi is None or (v <= hi).all())
    assert model.version.startswith(kind + "-")
    assert model.meta["features"], "metadata must list the features in order"


def test_forecast_tolerates_missing_history(small_run, models_dir):
    """Right after a run starts there is no 'yesterday': lags are missing."""
    _need("traffic", models_dir)
    model = ForecastModel.load("traffic", models_dir)
    meta = CityMeta.from_city(small_run["city"])
    g = Grid.from_telemetry(small_run["telemetry"][:4], meta)
    out = model.predict_last(g)
    assert all(np.isfinite(v).all() for v in out.values())


def test_anomaly_scores_every_entity(small_run, models_dir):
    _need("anomaly", models_dir)
    model = AnomalyModel.load(models_dir)
    meta = CityMeta.from_city(small_run["city"])
    g = Grid.from_telemetry(small_run["telemetry"][-model.history_steps :], meta)
    out = model.score_last(g)
    assert len(out["traffic"]) == meta.n_roads
    assert len(out["energy"]) == meta.n_districts
    assert len(out["air"]) == meta.n_stations
    for entries in out.values():
        scores = [e["score"] for e in entries]
        assert scores == sorted(scores, reverse=True)
        assert all(e["reason"] for e in entries)


# ------------------------------------------------------------------ service

@pytest.fixture
def client(monkeypatch, models_dir, small_run):
    import fakeredis.aioredis
    from fastapi.testclient import TestClient

    from nexus_ml import service

    for kind in ("traffic", "pm25"):
        _need(kind, models_dir)
    fake = fakeredis.aioredis.FakeRedis()
    monkeypatch.setattr(service.aioredis, "from_url", lambda url: fake)
    app = service.create_app(["traffic", "pm25"])
    with TestClient(app) as c:
        yield c


def test_predict_endpoint_matches_forecast_schema(client, small_run):
    body = {"city": small_run["city"], "telemetry": small_run["telemetry"][-300:]}
    r = client.post("/predict/traffic", json=body)
    assert r.status_code == 200, r.text
    p = r.json()
    # Field names are the contract with the Go gateway (events.Forecast).
    for key in ("model", "version", "entity_type", "horizon_unit", "horizons", "target_ticks", "issued_tick",
                "entities", "values", "latency_ms"):
        assert key in p, key
    assert p["entity_type"] == "road" and p["horizons"] == [15, 30, 60]
    assert p["target_ticks"] == [p["issued_tick"] + h * 60 for h in p["horizons"]]
    assert len(p["values"]["30"]) == len(p["entities"]) == len(small_run["city"]["roads"])


def test_predict_endpoint_validates_input(client, small_run):
    assert client.post("/predict/traffic", json={"telemetry": []}).status_code == 422
    off_grid = [dict(small_run["telemetry"][0], tick=61)]
    assert client.post("/predict/traffic", json={"city": small_run["city"], "telemetry": off_grid}).status_code == 422
    broken = [{"tick": 300, "roads": "nope"}]
    assert client.post("/predict/traffic", json={"city": small_run["city"], "telemetry": broken}).status_code == 422
    assert client.post("/predict/energy", json={"city": small_run["city"], "telemetry": small_run["telemetry"]}).status_code == 404


def test_health_and_models(client):
    assert client.get("/health").json() == {"status": "ok"}
    models = client.get("/models").json()
    assert set(models) == {"traffic", "pm25"}
    assert all(m["version"] for m in models.values())
    # Not ready: no simulation is publishing telemetry in this test.
    assert client.get("/ready").status_code == 503


def test_metrics_endpoint(client, small_run):
    client.get("/health")
    client.post("/predict/traffic", json={"city": small_run["city"], "telemetry": small_run["telemetry"][-300:]})
    text = client.get("/metrics").text
    assert 'nexus_ml_http_requests_total{code="200",route="/predict/{kind}"}' in text
    assert 'nexus_ml_inference_seconds_count{model="traffic",source="predict"}' in text
    assert 'nexus_ml_model_loaded{model="traffic"} 1.0' in text
    assert "/metrics" not in [line.split('route="')[1].split('"')[0] for line in text.splitlines() if 'route="' in line]
