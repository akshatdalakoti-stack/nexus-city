"""A dense [time × entity] view of simulation telemetry on the 5-minute grid.

The same Grid is built from a generated dataset (training) or from live
telemetry messages (inference), so feature code never needs to know which.
Missing time steps become NaN rows; missing sensor readings are NaN.
"""

from __future__ import annotations

import json
import warnings
from dataclasses import dataclass, field
from pathlib import Path

import numpy as np

from .citymeta import CityMeta
from .config import EPOCH_MINUTE_OF_DAY, STEP_S

STATUS_CODE = {"ok": 0, "stuck": 1, "offline": 2}

ROAD_FIELDS = ("congestion", "speed_kmh", "flow_vph", "vehicles", "incident")
DISTRICT_FIELDS = ("demand_mw", "utilization", "pm25", "vehicles")
CITY_FIELDS = ("temp_c", "humidity", "wind_speed_ms", "wind_dir_deg", "vehicles", "avg_congestion", "demand_mw")


@dataclass
class Grid:
    ticks: np.ndarray  # [T] simulated seconds, contiguous steps of STEP_S
    road: dict[str, np.ndarray]  # field -> [T, R]
    district: dict[str, np.ndarray]  # field -> [T, D]
    station_pm: np.ndarray  # [T, S] valid readings only (stuck/offline -> NaN)
    station_raw: np.ndarray  # [T, S] as reported (stuck sensors repeat; offline -> NaN)
    station_status: np.ndarray  # [T, S] 0 ok, 1 stuck, 2 offline, -1 no data
    city: dict[str, np.ndarray]  # field -> [T]
    meta: CityMeta
    events: list[dict] = field(default_factory=list)

    @property
    def T(self) -> int:
        return len(self.ticks)

    # ---- time helpers -------------------------------------------------
    @property
    def minute_of_day(self) -> np.ndarray:
        return (self.ticks // 60 + EPOCH_MINUTE_OF_DAY) % 1440

    @property
    def day_index(self) -> np.ndarray:
        """Simulated days since the epoch (the epoch itself is day 0)."""
        return self.ticks // 86400

    @property
    def weekday(self) -> np.ndarray:
        """0 = Monday. The epoch is a Monday at 05:30."""
        return ((self.ticks // 60 + EPOCH_MINUTE_OF_DAY) // 1440) % 7

    # ---- construction -------------------------------------------------
    @classmethod
    def empty(cls, ticks: np.ndarray, meta: CityMeta) -> "Grid":
        T, R, D, S = len(ticks), meta.n_roads, meta.n_districts, meta.n_stations
        nan = lambda *shape: np.full(shape, np.nan, dtype=np.float32)  # noqa: E731
        return cls(
            ticks=ticks.astype(np.int64),
            road={f: nan(T, R) for f in ROAD_FIELDS},
            district={f: nan(T, D) for f in DISTRICT_FIELDS},
            station_pm=nan(T, S),
            station_raw=nan(T, S),
            station_status=np.full((T, S), -1, dtype=np.int8),
            city={f: nan(T) for f in CITY_FIELDS},
            meta=meta,
        )

    @staticmethod
    def tick_range(first: int, last: int) -> np.ndarray:
        return np.arange(first, last + STEP_S, STEP_S, dtype=np.int64)

    @classmethod
    def from_dataset(cls, path: Path) -> "Grid":
        """Load a dataset directory written by `headless -dataset-dir`."""
        path = Path(path)
        cache = path / "grid.npz"
        meta = CityMeta.from_city(json.loads((path / "city.json").read_text()))
        if cache.exists() and cache.stat().st_mtime >= (path / "manifest.json").stat().st_mtime:
            z = np.load(cache)
            g = cls(
                ticks=z["ticks"],
                road={f: z[f"road_{f}"] for f in ROAD_FIELDS},
                district={f: z[f"district_{f}"] for f in DISTRICT_FIELDS},
                station_pm=z["station_pm"],
                station_raw=z["station_raw"],
                station_status=z["station_status"],
                city={f: z[f"city_{f}"] for f in CITY_FIELDS},
                meta=meta,
            )
        else:
            g = cls._parse_csvs(path, meta)
            np.savez_compressed(
                cache,
                ticks=g.ticks,
                station_pm=g.station_pm,
                station_raw=g.station_raw,
                station_status=g.station_status,
                **{f"road_{f}": v for f, v in g.road.items()},
                **{f"district_{f}": v for f, v in g.district.items()},
                **{f"city_{f}": v for f, v in g.city.items()},
            )
        g.events = [json.loads(line) for line in (path / "events.jsonl").read_text().splitlines() if line]
        return g

    @classmethod
    def _parse_csvs(cls, path: Path, meta: CityMeta) -> "Grid":
        # Imported here: only training reads CSV datasets, and the inference
        # image ships without pandas/pyarrow (~250 MB).
        import pandas as pd

        city = pd.read_csv(path / "city.csv")
        ticks = cls.tick_range(int(city.tick.min()), int(city.tick.max()))
        g = cls.empty(ticks, meta)
        row = {int(t): i for i, t in enumerate(ticks)}

        roads = pd.read_csv(path / "roads.csv")
        ti = roads.tick.map(row).to_numpy()
        ri = roads.road.to_numpy()
        for f in ROAD_FIELDS:
            g.road[f][ti, ri] = roads[f].to_numpy(dtype=np.float32)

        dist = pd.read_csv(path / "districts.csv")
        ti, di = dist.tick.map(row).to_numpy(), dist.district.to_numpy()
        for f in DISTRICT_FIELDS:
            g.district[f][ti, di] = dist[f].to_numpy(dtype=np.float32)

        st = pd.read_csv(path / "stations.csv")
        ti, si = st.tick.map(row).to_numpy(), st.station.to_numpy()
        status = st.status.map(STATUS_CODE).to_numpy(dtype=np.int8)
        raw = st.pm25.to_numpy(dtype=np.float32)
        g.station_status[ti, si] = status
        g.station_raw[ti, si] = raw
        g.station_pm[ti, si] = np.where(status == 0, raw, np.nan)

        ti = city.tick.map(row).to_numpy()
        for f in CITY_FIELDS:
            g.city[f][ti] = city[f].to_numpy(dtype=np.float32)
        return g

    @classmethod
    def from_samples(cls, samples: list["Sample"], meta: CityMeta) -> "Grid":
        """Build a grid from compact samples (see `Sample.from_payload`).
        Gaps in the 5-minute grid become NaN rows."""
        samples = sorted(samples, key=lambda s: s.tick)
        if not samples:
            raise ValueError("no telemetry on the 5-minute grid")
        ticks = cls.tick_range(samples[0].tick, samples[-1].tick)
        g = cls.empty(ticks, meta)
        for s in samples:
            t = (s.tick - int(ticks[0])) // STEP_S
            for f in ROAD_FIELDS:
                g.road[f][t] = s.road[f]
            for f in DISTRICT_FIELDS:
                g.district[f][t] = s.district[f]
            for f in CITY_FIELDS:
                g.city[f][t] = s.city[f]
            g.station_status[t] = s.station_status
            g.station_raw[t] = s.station_raw
            g.station_pm[t] = np.where(s.station_status == 0, s.station_raw, np.nan)
        return g

    @classmethod
    def from_telemetry(cls, payloads: list[dict], meta: CityMeta) -> "Grid":
        """Build a grid from live telemetry payloads (nexus.telemetry/v1),
        keeping only samples on the 5-minute grid."""
        return cls.from_samples([Sample.from_payload(p) for p in payloads if p["tick"] % STEP_S == 0], meta)


@dataclass
class Sample:
    """One telemetry snapshot reduced to the arrays the models use
    (~6 KB instead of ~32 KB of JSON), so services can keep days of history."""

    tick: int
    road: dict[str, np.ndarray]
    district: dict[str, np.ndarray]
    city: dict[str, float]
    station_raw: np.ndarray
    station_status: np.ndarray

    @classmethod
    def from_payload(cls, p: dict) -> "Sample":
        roads, dists, sts = p["roads"], p["districts"], p["stations"]
        w, s = p["weather"], p["summary"]
        return cls(
            tick=int(p["tick"]),
            road={
                "congestion": np.array([o["congestion"] for o in roads], dtype=np.float32),
                "speed_kmh": np.array([o["speed_kmh"] for o in roads], dtype=np.float32),
                "flow_vph": np.array([o["flow_vph"] for o in roads], dtype=np.float32),
                "vehicles": np.array([o["vehicles"] for o in roads], dtype=np.float32),
                "incident": np.array([1.0 if o.get("incident") else 0.0 for o in roads], dtype=np.float32),
            },
            district={f: np.array([o[f] for o in dists], dtype=np.float32) for f in DISTRICT_FIELDS},
            city={
                "temp_c": w["temp_c"], "humidity": w["humidity"], "wind_speed_ms": w["wind_speed_ms"],
                "wind_dir_deg": w["wind_dir_deg"], "vehicles": s["vehicles"],
                "avg_congestion": s["avg_congestion"], "demand_mw": s["demand_mw"],
            },
            station_raw=np.array([np.nan if o["pm25"] is None else o["pm25"] for o in sts], dtype=np.float32),
            station_status=np.array([STATUS_CODE.get(o["status"], 2) for o in sts], dtype=np.int8),
        )


def lag(a: np.ndarray, k: int) -> np.ndarray:
    """Value k steps earlier along axis 0 (NaN where unavailable)."""
    out = np.full_like(a, np.nan, dtype=np.float32)
    if k < len(a):
        out[k:] = a[: len(a) - k]
    return out


def lead(a: np.ndarray, k: int) -> np.ndarray:
    """Value k steps later along axis 0 (NaN where unavailable). Targets only."""
    out = np.full_like(a, np.nan, dtype=np.float32)
    if k < len(a):
        out[: len(a) - k] = a[k:]
    return out


def rolling_mean(a: np.ndarray, k: int) -> np.ndarray:
    """Trailing mean over the last k steps including now, ignoring missing values."""
    stack = np.stack([lag(a, i) for i in range(k)])
    with warnings.catch_warnings():
        warnings.simplefilter("ignore", category=RuntimeWarning)
        return np.nanmean(stack, axis=0).astype(np.float32)


def rolling_std(a: np.ndarray, k: int) -> np.ndarray:
    """Trailing standard deviation over the last k steps, ignoring missing values."""
    stack = np.stack([lag(a, i) for i in range(k)])
    with warnings.catch_warnings():
        warnings.simplefilter("ignore", category=RuntimeWarning)
        return np.nanstd(stack, axis=0).astype(np.float32)
