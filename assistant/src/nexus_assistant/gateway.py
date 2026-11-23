"""Client for the Nexus City gateway's public REST API.

The assistant reads exactly what the dashboard reads: it has no private
access to the simulation, the models or the database.
"""

from __future__ import annotations

import asyncio
from datetime import datetime, timedelta, timezone
from typing import Any

import httpx

EPOCH = datetime(2026, 6, 1, 5, 30, tzinfo=timezone.utc)


class GatewayError(RuntimeError):
    """The gateway could not answer (down, or the data does not exist yet)."""


def tick_time(tick: int) -> datetime:
    return EPOCH + timedelta(seconds=int(tick))


def fmt_sim_time(iso_or_tick: str | int) -> str:
    """'Tue 17:30' style label for a simulated time."""
    t = tick_time(iso_or_tick) if isinstance(iso_or_tick, int) else datetime.fromisoformat(iso_or_tick.replace("Z", "+00:00"))
    return t.strftime("%a %H:%M")


class Gateway:
    def __init__(self, base_url: str, client: httpx.AsyncClient | None = None, timeout: float = 15.0):
        self.base_url = base_url.rstrip("/")
        self._client = client or httpx.AsyncClient(timeout=timeout)
        self._city: dict | None = None
        self._city_run: str | None = None

    async def close(self) -> None:
        await self._client.aclose()

    async def get(self, path: str, **params: Any) -> Any:
        try:
            r = await self._client.get(self.base_url + path, params=params or None)
        except httpx.HTTPError as e:
            raise GatewayError(f"cannot reach the gateway: {e}") from e
        if r.status_code >= 400:
            try:
                msg = r.json().get("error", r.text)
            except ValueError:
                msg = r.text
            raise GatewayError(f"{path}: {r.status_code} {msg}")
        return r.json()

    async def post(self, path: str, body: dict) -> Any:
        try:
            r = await self._client.post(self.base_url + path, json=body)
        except httpx.HTTPError as e:
            raise GatewayError(f"cannot reach the gateway: {e}") from e
        if r.status_code >= 400:
            try:
                msg = r.json().get("error", r.text)
            except ValueError:
                msg = r.text
            raise GatewayError(f"{path}: {r.status_code} {msg}")
        return r.json()

    # ---- typed helpers ---------------------------------------------------
    async def telemetry(self) -> dict:
        """Latest per-minute observation (the payload of the envelope)."""
        return (await self.get("/api/telemetry/latest"))["payload"]

    async def telemetry_at(self, sim_time: datetime) -> dict | None:
        try:
            env = await self.get("/api/telemetry", at=sim_time.strftime("%Y-%m-%dT%H:%M:%SZ"))
        except GatewayError:
            return None
        return env["payload"]

    async def city(self) -> dict:
        clock = await self.get("/api/clock")
        run = clock.get("run_id")
        if self._city is None or run != self._city_run:
            self._city = await self.get("/api/city")
            self._city_run = run
        return self._city

    async def clock(self) -> dict:
        return (await self.get("/api/clock"))["payload"]

    async def forecasts(self) -> dict[str, dict]:
        """Latest forecast payload per model (missing models are absent)."""
        envs = await self.get("/api/forecasts/latest")
        return {k: v["payload"] for k, v in envs.items()}

    async def anomalies(self) -> dict | None:
        try:
            return (await self.get("/api/anomalies/latest"))["payload"]
        except GatewayError:
            return None

    async def models(self) -> list[dict]:
        return await self.get("/api/models")

    async def model_meta(self, kind: str) -> dict | None:
        try:
            return await self.get(f"/api/models/{kind}")
        except GatewayError:
            return None

    async def create_scenario(self, body: dict) -> dict:
        return await self.post("/api/scenarios", body)

    async def scenario(self, sid: str) -> dict:
        return await self.get(f"/api/scenarios/{sid}")

    async def wait_scenario(self, sid: str, timeout_s: float = 90, poll_s: float = 0.5) -> dict:
        """Poll until a scenario finishes (or the timeout passes)."""
        loop = asyncio.get_running_loop()
        deadline = loop.time() + timeout_s
        while True:
            sc = await self.scenario(sid)
            if sc["status"] in ("done", "failed") or loop.time() > deadline:
                return sc
            await asyncio.sleep(poll_s)
