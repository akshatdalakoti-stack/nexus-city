"""Feature definitions shared by training and live inference.

Every feature at time index t uses only data at or before t. Targets are the
only things that look ahead (`lead`), and they are built separately. The
test `test_features_do_not_see_the_future` enforces this.

Each builder returns a dict of [T, N] float32 arrays (T time steps, N
entities). `rows()` flattens the selected time steps into a feature matrix.
"""

from __future__ import annotations

import warnings

import numpy as np

from .config import STEPS_PER_DAY, STEPS_PER_HOUR
from .grid import Grid, lag, lead, rolling_mean

Feats = dict[str, np.ndarray]


def _static(v: np.ndarray, T: int) -> np.ndarray:
    return np.broadcast_to(np.asarray(v, dtype=np.float32)[None, :], (T, len(v)))


def _time(g: Grid, n: int, offset_steps: int = 0, prefix: str = "") -> Feats:
    minute = (g.minute_of_day + offset_steps * 5) % 1440
    days_ahead = (g.minute_of_day + offset_steps * 5) // 1440
    weekday = (g.weekday + days_ahead) % 7
    ang = 2 * np.pi * minute / 1440
    return {
        f"{prefix}tod_sin": _static_t(np.sin(ang), n),
        f"{prefix}tod_cos": _static_t(np.cos(ang), n),
        f"{prefix}weekday": _static_t(weekday.astype(np.float32), n),
        f"{prefix}weekend": _static_t((weekday >= 5).astype(np.float32), n),
    }


def _static_t(v: np.ndarray, n: int) -> np.ndarray:
    return np.broadcast_to(np.asarray(v, dtype=np.float32)[:, None], (len(v), n))


def forward_fill(a: np.ndarray) -> tuple[np.ndarray, np.ndarray]:
    """Carry the last valid value forward in time. Also returns how many
    steps old that value is (NaN if there has never been one)."""
    T = len(a)
    idx = np.where(np.isnan(a), 0, np.arange(T)[:, None])
    np.maximum.accumulate(idx, axis=0, out=idx)
    filled = a[idx, np.arange(a.shape[1])[None, :]]
    age = (np.arange(T)[:, None] - idx).astype(np.float32)
    never = np.isnan(filled)
    age[never] = np.nan
    return filled.astype(np.float32), age


def rows(feats: Feats, names: list[str], t_idx: np.ndarray) -> np.ndarray:
    """Stack the chosen time steps into an [len(t_idx) * N, F] matrix
    (time-major: all entities at the first step, then the next step...)."""
    return np.stack([feats[n][t_idx].reshape(-1) for n in names], axis=1).astype(np.float32)


def target_rows(target: np.ndarray, t_idx: np.ndarray) -> np.ndarray:
    return target[t_idx].reshape(-1).astype(np.float32)


# ---------------------------------------------------------------- traffic

def traffic_features(g: Grid, horizon_steps: int) -> Feats:
    m = g.meta
    R, T = m.n_roads, g.T
    C = g.road["congestion"]
    speed_ratio = g.road["speed_kmh"] / m.road_speed[None, :]
    flow_lane = g.road["flow_vph"] / m.road_lanes[None, :] / 1000
    inc = g.road["incident"]
    delta15 = C - lag(C, 3)

    nbrs = [np.nonzero(m.road_adj[i])[0] for i in range(R)]
    with warnings.catch_warnings():
        warnings.simplefilter("ignore", category=RuntimeWarning)
        nbr_max = np.stack([np.nanmax(C[:, n], axis=1) for n in nbrs], axis=1)
        nbr_inc = np.stack([np.nanmax(inc[:, n], axis=1) for n in nbrs], axis=1)
    member = np.zeros((m.n_districts, R), dtype=np.float32)
    member[m.road_district, np.arange(R)] = 1
    member /= member.sum(axis=1, keepdims=True)
    dist_cong = np.nan_to_num(C) @ member.T  # [T, D]

    f: Feats = {
        "cong": C,
        "speed_ratio": speed_ratio.astype(np.float32),
        "flow_per_lane": flow_lane.astype(np.float32),
        "incident": inc,
        "cong_lag5": lag(C, 1),
        "cong_lag15": lag(C, 3),
        "cong_lag30": lag(C, 6),
        "cong_lag60": lag(C, 12),
        "cong_mean30": rolling_mean(C, 6),
        "cong_delta15": delta15,
        "cong_yday_now": lag(C, STEPS_PER_DAY),
        "cong_yday_target": lag(C, STEPS_PER_DAY - horizon_steps),
        "nbr_cong_mean": (np.nan_to_num(C) @ m.road_adj.T).astype(np.float32),
        "nbr_cong_max": nbr_max.astype(np.float32),
        "nbr_delta15": (np.nan_to_num(delta15) @ m.road_adj.T).astype(np.float32),
        "nbr_incident": nbr_inc.astype(np.float32),
        "district_cong": dist_cong[:, m.road_district].astype(np.float32),
        "city_cong": _static_t(g.city["avg_congestion"], R),
        "city_vehicles_k": _static_t(g.city["vehicles"] / 1000, R),
        "road_class": _static(m.road_class, T),
        "lanes": _static(m.road_lanes, T),
        "speed_limit": _static(m.road_speed, T),
        "length_km": _static(m.road_length / 1000, T),
        "district_type": _static(m.road_dtype, T),
        "temp_c": _static_t(g.city["temp_c"], R),
    }
    f |= _time(g, R)
    f |= _time(g, R, horizon_steps, "target_")
    return f


def traffic_target(g: Grid, horizon_steps: int) -> np.ndarray:
    return lead(g.road["congestion"], horizon_steps)


# ----------------------------------------------------------------- energy

def energy_features(g: Grid, horizon_h: int) -> Feats:
    """District demand features, normalised by each district's base load so
    one model serves districts of very different size."""
    m = g.meta
    D, T = m.n_districts, g.T
    hs = horizon_h * STEPS_PER_HOUR
    base = m.district_base_mw[None, :]
    dem = g.district["demand_mw"] / base
    temp = _static_t(g.city["temp_c"], D)
    f: Feats = {
        "demand": dem,
        "demand_lag1h": lag(dem, STEPS_PER_HOUR),
        "demand_lag2h": lag(dem, 2 * STEPS_PER_HOUR),
        "demand_lag3h": lag(dem, 3 * STEPS_PER_HOUR),
        "demand_mean1h": rolling_mean(dem, STEPS_PER_HOUR),
        "demand_lag24h": lag(dem, STEPS_PER_DAY),
        "naive24_target": lag(dem, STEPS_PER_DAY - hs),  # same clock time yesterday as the target
        "naive168_target": lag(dem, 7 * STEPS_PER_DAY - hs),  # same time last week
        "temp_c": temp,
        "temp_lag24h": lag(temp, STEPS_PER_DAY),
        "temp_yday_target": lag(temp, STEPS_PER_DAY - hs),
        "horizon_h": np.full((T, D), horizon_h, dtype=np.float32),
        "district_type": _static(m.district_dtype, T),
        "base_mw": _static(m.district_base_mw, T),
        "capacity_ratio": _static(m.district_capacity_mw / m.district_base_mw, T),
        "pop_k": _static(m.district_pop / 1000, T),
        "jobs_k": _static(m.district_jobs / 1000, T),
        "density": _static(m.district_density, T),
    }
    f |= _time(g, D)
    f |= _time(g, D, hs, "target_")
    return f


def energy_target(g: Grid, horizon_h: int) -> np.ndarray:
    """Target: demand at t + h, as a multiple of base load."""
    return lead(g.district["demand_mw"] / g.meta.district_base_mw[None, :], horizon_h * STEPS_PER_HOUR)


# ------------------------------------------------------------------ PM2.5

def pm25_features(g: Grid, horizon_steps: int) -> Feats:
    m = g.meta
    S, T = m.n_stations, g.T
    P = g.station_pm  # valid readings only
    last, age = forward_fill(P)
    with warnings.catch_warnings():
        warnings.simplefilter("ignore", category=RuntimeWarning)
        others = np.stack([np.nanmedian(np.delete(P, i, axis=1), axis=1) for i in range(S)], axis=1)
    others_delta = others - lag(others, 3)

    flow = g.road["flow_vph"]
    member = np.zeros((m.n_districts, m.n_roads), dtype=np.float32)
    member[m.road_district, np.arange(m.n_roads)] = 1
    dist_flow = (np.nan_to_num(flow) @ member.T) / 1000  # [T, D] thousand veh/h
    dist_cong = (np.nan_to_num(g.road["congestion"]) @ (member / member.sum(1, keepdims=True)).T)

    # Is the station downwind of industry? Bearing from the station to the
    # nearest industrial district, compared with where the wind comes from.
    d = m.industrial_xy[None, :, :] - m.station_xy[:, None, :]  # [S, K, 2]
    dist = np.hypot(d[..., 0], d[..., 1])
    k = dist.argmin(axis=1)
    near = d[np.arange(S), k]
    bearing = np.degrees(np.arctan2(near[:, 0], near[:, 1]))  # compass bearing station -> industry
    ind_dist_km = dist[np.arange(S), k] / 1000
    wind_from = g.city["wind_dir_deg"]
    align = np.cos(np.radians(wind_from[:, None] - bearing[None, :])).astype(np.float32)
    status = g.station_status
    f: Feats = {
        "pm_last_valid": last,
        "pm_age_steps": age,
        "pm": P,
        "pm_lag15": lag(P, 3),
        "pm_lag30": lag(P, 6),
        "pm_lag60": lag(P, 12),
        "pm_mean30": rolling_mean(P, 6),
        "pm_delta15": P - lag(P, 3),
        "sensor_stuck": (status == 1).astype(np.float32),
        "sensor_offline": (status == 2).astype(np.float32),
        "others_median": others.astype(np.float32),
        "others_delta15": others_delta.astype(np.float32),
        "district_flow_k": dist_flow[:, m.station_district].astype(np.float32),
        "district_cong": dist_cong[:, m.station_district].astype(np.float32),
        "wind_speed": _static_t(g.city["wind_speed_ms"], S),
        "wind_from_sin": _static_t(np.sin(np.radians(wind_from)), S),
        "wind_from_cos": _static_t(np.cos(np.radians(wind_from)), S),
        "downwind_industry": align,
        "industry_km": _static(ind_dist_km, T),
        "downwind_x_near": (align * np.exp(-ind_dist_km / 3.0)[None, :]).astype(np.float32),
        "humidity": _static_t(g.city["humidity"], S),
        "temp_c": _static_t(g.city["temp_c"], S),
        "station_district_type": _static(m.district_dtype[m.station_district], T),
    }
    f |= _time(g, S)
    f |= _time(g, S, horizon_steps, "target_")
    return f


def pm25_target(g: Grid, horizon_steps: int) -> np.ndarray:
    """Target: the valid reading at t + h (NaN when the sensor is faulty,
    so those rows are dropped from training and evaluation)."""
    return lead(g.station_pm, horizon_steps)
