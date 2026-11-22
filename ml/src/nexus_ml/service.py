"""Inference service (FastAPI).

One process can host any subset of models: `nexus-ml serve --models traffic`.
Compose and Kubernetes run one model per container; local development can
run them all in one process.

Live loop: follow the `nexus:telemetry` stream, keep compact 5-minute samples
for the current run, and after each new sample publish forecasts to
`nexus:forecasts` (energy: on the hour) and anomaly scores to
`nexus:anomalies`. Every message uses the shared envelope.

HTTP: /health, /ready, /models, /forecast/{kind}/latest,
/anomalies/latest, and POST /predict/{kind} (forecast from telemetry
supplied in the request — the inference interface for scenarios and tests).
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import socket
import time
from collections import deque
from contextlib import asynccontextmanager
from datetime import datetime, timezone

import numpy as np
import redis.asyncio as aioredis
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

from . import prom
from .anomaly import AnomalyModel
from .citymeta import CityMeta
from .config import MODELS_DIR, STEP_S, tick_to_time
from .forecast import SPECS, ForecastModel
from .grid import Grid, Sample

log = logging.getLogger("nexus_ml.service")

STREAM_TELEMETRY = "nexus:telemetry"
STREAM_FORECASTS = "nexus:forecasts"
STREAM_ANOMALIES = "nexus:anomalies"
KEY_CITY = "nexus:city"
KEY_MODELS = "nexus:models"
KEY_MODEL_META = "nexus:model_meta:"
FORECAST_MAXLEN = 3000
ANOMALY_MAXLEN = 1000
ENTITY_IDS = {"road": "road_ids", "district": "district_ids", "station": "station_ids"}


def _now() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def envelope(schema: str, run_id: str, seed: int, tick: int, payload: dict) -> dict:
    return {
        "schema": schema,
        "run_id": run_id,
        "seed": seed,
        "tick": tick,
        "sim_time": tick_to_time(tick).isoformat().replace("+00:00", "Z"),
        "emitted_at": _now(),
        "payload": payload,
    }


def forecast_payload(model: ForecastModel, meta: CityMeta, tick: int, values: dict[int, np.ndarray], latency_ms: float) -> dict:
    ids = getattr(meta, ENTITY_IDS[model.spec.entity])
    unit_s = 60 if model.spec.unit == "min" else 3600
    return {
        "model": model.kind,
        "version": model.version,
        "entity_type": model.spec.entity,
        "horizon_unit": model.spec.unit,
        "horizons": list(values),
        "target_ticks": [tick + h * unit_s for h in values],
        "output_unit": model.spec.output_unit,
        "issued_tick": tick,
        "entities": ids,
        "values": {str(h): [round(float(x), 4) for x in v] for h, v in values.items()},
        "latency_ms": round(latency_ms, 2),
        "synthetic": True,
    }


class Runtime:
    """Models, the telemetry buffer and the latest outputs for one process."""

    def __init__(self, kinds: list[str]):
        self.kinds = kinds
        self.models: dict[str, ForecastModel] = {}
        self.anomaly: AnomalyModel | None = None
        self.errors: dict[str, str] = {}
        for k in kinds:
            try:
                if k == "anomaly":
                    self.anomaly = AnomalyModel.load(MODELS_DIR)
                else:
                    self.models[k] = ForecastModel.load(k, MODELS_DIR)
            except FileNotFoundError as e:
                self.errors[k] = str(e)
        self.meta: CityMeta | None = None
        self.run_id = ""
        self.seed = 0
        steps = [m.history_steps for m in self.models.values()] + ([self.anomaly.history_steps] if self.anomaly else [])
        self.buffer: deque[Sample] = deque(maxlen=max(steps, default=300) + 1)
        self.latest: dict[str, dict] = {}
        self.latency: dict[str, float] = {}
        self.consuming = False
        self.last_tick = -1

    def model_info(self) -> dict[str, dict]:
        out = {}
        for k, m in self.models.items():
            out[k] = {"kind": k, "version": m.version, "description": m.meta["description"], "horizons": m.meta["horizons"],
                      "horizon_unit": m.meta["horizon_unit"], "entity": m.meta["entity"], "output_unit": m.meta["output_unit"]}
        if self.anomaly:
            out["anomaly"] = {"kind": "anomaly", "version": self.anomaly.version, "description": self.anomaly.meta["description"],
                              "domains": list(self.anomaly.detectors)}
        for k, err in self.errors.items():
            out[k] = {"kind": k, "version": None, "error": err}
        return out

    # ---- inference ----------------------------------------------------
    def grid(self, steps: int) -> Grid:
        assert self.meta is not None
        samples = list(self.buffer)[-steps:]
        return Grid.from_samples(samples, self.meta)

    def run_models(self, tick: int) -> list[tuple[str, dict]]:
        """Run every model due at this tick. Returns (stream, envelope) pairs."""
        out = []
        for kind, model in self.models.items():
            if kind == "energy" and tick % 3600 != 0 and kind in self.latest:
                continue  # energy forecasts are issued hourly
            t0 = time.perf_counter()
            values = model.predict_last(self.grid(model.history_steps))
            ms = (time.perf_counter() - t0) * 1000
            self.latency[kind] = ms
            prom.INFERENCE_SECONDS.labels(kind, "live").observe(ms / 1000)
            env = envelope("nexus.forecast/v1", self.run_id, self.seed, tick, forecast_payload(model, self.meta, tick, values, ms))
            self.latest[kind] = env
            out.append((STREAM_FORECASTS, env))
        if self.anomaly:
            t0 = time.perf_counter()
            scored = self.anomaly.score_last(self.grid(self.anomaly.history_steps))
            ms = (time.perf_counter() - t0) * 1000
            self.latency["anomaly"] = ms
            prom.INFERENCE_SECONDS.labels("anomaly", "live").observe(ms / 1000)
            payload = {
                "model": "anomaly",
                "version": self.anomaly.version,
                "issued_tick": tick,
                "flagged": [e for es in scored.values() for e in es if e["anomalous"]],
                "top": {d: es[:3] for d, es in scored.items()},
                "latency_ms": round(ms, 2),
                "synthetic": True,
            }
            env = envelope("nexus.anomaly/v1", self.run_id, self.seed, tick, payload)
            self.latest["anomaly"] = env
            out.append((STREAM_ANOMALIES, env))
        return out


async def _load_city(r: aioredis.Redis, rt: Runtime) -> None:
    raw = await r.get(KEY_CITY)
    if raw is None:
        raise RuntimeError("no city in Redis yet (is the simulation worker running?)")
    city = json.loads(raw)
    rt.meta = CityMeta.from_city(city)
    rt.seed = rt.meta.seed


async def _publish(r: aioredis.Redis, items: list[tuple[str, dict]]) -> None:
    for stream, env in items:
        maxlen = FORECAST_MAXLEN if stream == STREAM_FORECASTS else ANOMALY_MAXLEN
        await r.xadd(stream, {"data": json.dumps(env)}, maxlen=maxlen, approximate=True)
        prom.PUBLISHED.labels(env["payload"].get("model", "unknown")).inc()


async def _register(r: aioredis.Redis, rt: Runtime) -> None:
    """Report this process's models and their training metadata. Keys
    expire if the service stops, so the dashboard shows real liveness."""
    _update_gauges(rt)
    host = socket.gethostname()
    for kind, info in rt.model_info().items():
        info = {**info, "host": host, "updated_at": _now(), "latency_ms": rt.latency.get(kind),
                "run_id": rt.run_id, "last_tick": rt.last_tick, "buffer_samples": len(rt.buffer)}
        await r.hset(KEY_MODELS, kind, json.dumps(info))
        meta = rt.models[kind].meta if kind in rt.models else (rt.anomaly.meta if kind == "anomaly" and rt.anomaly else None)
        if meta is not None:
            await r.set(KEY_MODEL_META + kind, json.dumps(meta), ex=120)
    await r.expire(KEY_MODELS, 60)


async def consume(r: aioredis.Redis, rt: Runtime) -> None:
    """Follow the telemetry stream forever, restarting after errors."""
    while True:
        try:
            await _consume_once(r, rt)
        except asyncio.CancelledError:
            raise
        except Exception as e:  # keep serving; Redis or the sim may restart
            rt.consuming = False
            log.warning("telemetry consumer error: %s; retrying", e)
            await asyncio.sleep(2)


async def _consume_once(r: aioredis.Redis, rt: Runtime) -> None:
    await _load_city(r, rt)
    # Bootstrap from the history already in the stream (newest last).
    history = await r.xrevrange(STREAM_TELEMETRY, "+", "-", count=rt.buffer.maxlen * 5)
    history.reverse()
    last_id = history[-1][0] if history else "$"
    for _id, fields in history:
        env = json.loads(fields[b"data"] if b"data" in fields else fields["data"])
        _accept(rt, env)
    if rt.buffer:
        await _publish(r, await asyncio.to_thread(rt.run_models, rt.last_tick))
    rt.consuming = True
    log.info("following telemetry: run %s, %d samples buffered", rt.run_id, len(rt.buffer))
    last_register = 0.0
    while True:
        res = await r.xread({STREAM_TELEMETRY: last_id}, block=5000, count=50)
        for _stream, msgs in res or []:
            for msg_id, fields in msgs:
                last_id = msg_id
                env = json.loads(fields[b"data"] if b"data" in fields else fields["data"])
                if env["run_id"] != rt.run_id and rt.run_id:
                    log.info("new simulation run %s", env["run_id"])
                    rt.buffer.clear()
                    rt.latest.clear()
                    await _load_city(r, rt)
                if _accept(rt, env):
                    await _publish(r, await asyncio.to_thread(rt.run_models, env["tick"]))
        if time.monotonic() - last_register > 5:
            await _register(r, rt)
            last_register = time.monotonic()


def _update_gauges(rt: Runtime) -> None:
    for kind, info in rt.model_info().items():
        prom.MODEL_LOADED.labels(kind).set(1 if info.get("version") else 0)
    prom.BUFFER_SAMPLES.set(len(rt.buffer))
    prom.CONSUMING.set(1 if rt.consuming else 0)


def _accept(rt: Runtime, env: dict) -> bool:
    """Add a telemetry envelope to the buffer if it is on the 5-minute grid."""
    rt.run_id = env["run_id"]
    tick = int(env["tick"])
    if tick % STEP_S != 0 or tick <= rt.last_tick:
        return False
    rt.buffer.append(Sample.from_payload(env["payload"]))
    rt.last_tick = tick
    return True


class PredictRequest(BaseModel):
    """Telemetry payloads (nexus.telemetry/v1 `payload` objects), oldest first.
    Samples off the 5-minute grid are ignored; gaps are treated as missing."""

    city: dict | None = Field(default=None, description="City JSON; defaults to the live city")
    telemetry: list[dict] = Field(min_length=1)


def create_app(kinds: list[str]) -> FastAPI:
    unknown = set(kinds) - {*SPECS, "anomaly"}
    if unknown:
        raise SystemExit(f"unknown models: {sorted(unknown)}")
    rt = Runtime(kinds)
    redis_url = os.environ.get("REDIS_URL", "redis://localhost:6379/0")

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        r = aioredis.from_url(redis_url)
        task = asyncio.create_task(consume(r, rt))
        yield
        task.cancel()
        await r.aclose()

    app = FastAPI(title="Nexus City inference", version="0.2.0", lifespan=lifespan)
    prom.install(app)
    _update_gauges(rt)

    @app.get("/health")
    def health():
        return {"status": "ok"}

    @app.get("/ready")
    def ready():
        problems = dict(rt.errors)
        if not rt.consuming:
            problems["telemetry"] = "not following the telemetry stream yet"
        if problems:
            raise HTTPException(503, detail=problems)
        return {"status": "ready", "models": list(rt.model_info())}

    @app.get("/models")
    def models():
        return rt.model_info()

    @app.get("/forecast/{kind}/latest")
    def latest(kind: str):
        if kind not in rt.models:
            raise HTTPException(404, f"model {kind!r} is not served here")
        if kind not in rt.latest:
            raise HTTPException(503, "no forecast yet")
        return rt.latest[kind]

    @app.get("/anomalies/latest")
    def anomalies():
        if "anomaly" not in rt.latest:
            raise HTTPException(503, "no anomaly scores yet")
        return rt.latest["anomaly"]

    @app.post("/predict/{kind}")
    def predict(kind: str, req: PredictRequest):
        model = rt.models.get(kind)
        if model is None:
            raise HTTPException(404, f"model {kind!r} is not served here")
        meta = CityMeta.from_city(req.city) if req.city else rt.meta
        if meta is None:
            raise HTTPException(503, "no city loaded; pass `city` in the request")
        on_grid = [p for p in req.telemetry if isinstance(p.get("tick"), int) and p["tick"] % STEP_S == 0]
        if not on_grid:
            raise HTTPException(422, "no telemetry sample is on the 5-minute grid (tick % 300 == 0)")
        try:
            g = Grid.from_telemetry(on_grid[-model.history_steps :], meta)
        except (KeyError, TypeError, IndexError, ValueError) as e:
            raise HTTPException(422, f"telemetry does not match nexus.telemetry/v1: {e}") from e
        t0 = time.perf_counter()
        values = model.predict_last(g)
        ms = (time.perf_counter() - t0) * 1000
        prom.INFERENCE_SECONDS.labels(kind, "predict").observe(ms / 1000)
        return forecast_payload(model, meta, int(g.ticks[-1]), values, ms)

    return app
