"""Tools the assistant uses to look things up. Each returns compact JSON
facts taken from the gateway, with provenance (simulated time, model
versions and their measured error, scenario ids). Both the LLM mode and the
rule-based mode answer only from these results.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from typing import Any, Awaitable, Callable

from .gateway import Gateway, GatewayError, fmt_sim_time, tick_time
from .resolve import find_district, find_roads

SYNTHETIC = "Synthetic values from the Nexus City simulation (a fictional city), not real observations."


def pct(x: float | None) -> int | None:
    return None if x is None else round(x * 100)


def r1(x: float | None) -> float | None:
    return None if x is None else round(x, 1)


def road_label(r: dict) -> str:
    return f"{r['name']} ({r['segment']})"


def phase_of_day(sim_iso: str) -> str:
    t = datetime.fromisoformat(sim_iso.replace("Z", "+00:00"))
    h, weekend = t.hour + t.minute / 60, t.weekday() >= 5
    if weekend:
        return "weekend"
    if 7 <= h < 10:
        return "weekday morning rush (07:00–10:00)"
    if 16 <= h < 20:
        return "weekday evening rush (16:00–20:00)"
    if h >= 22 or h < 6:
        return "night"
    return "weekday off-peak"


def length_weighted(city: dict, roads_obs: list[dict], idx: list[int]) -> float:
    total = sum(city["roads"][i]["length_m"] for i in idx) or 1
    return sum(roads_obs[i]["congestion"] * city["roads"][i]["length_m"] for i in idx) / total


async def _model_mae(gw: Gateway, kind: str) -> dict | None:
    meta = await gw.model_meta(kind)
    if not meta:
        return None
    test = meta.get("metrics", {}).get("test", {})
    out = {}
    for h, e in test.items():
        out[h] = {"model_mae": round(e["model"]["mae"], 4), "best_baseline": e["best_baseline"],
                  "baseline_mae": round(e[e["best_baseline"]]["mae"], 4), "improvement_pct": round(e["mae_improvement_pct"], 1)}
    return {"version": meta["version"], "test_mae_by_horizon": out}


# ------------------------------------------------------------------ tools

async def city_overview(gw: Gateway, **_: Any) -> dict:
    t, city = await gw.telemetry(), await gw.city()
    s, w = t["summary"], t["weather"]
    majors = [(o, city["roads"][i]) for i, o in enumerate(t["roads"]) if city["roads"][i]["class"] != "local"]
    busiest = sorted(majors, key=lambda x: -x[0]["congestion"])[:5]
    dists = sorted(zip(t["districts"], city["districts"]), key=lambda x: -x[0]["utilization"])[:3]
    stations = {f["id"]: f for f in city["facilities"] if f["kind"] == "aq_station"}
    ok = [o for o in t["stations"] if o["status"] == "ok" and o["pm25"] is not None]
    alerts = t.get("alerts") or []
    return {
        "sim_time": fmt_sim_time(t["sim_time"]),
        "phase": phase_of_day(t["sim_time"]),
        "weather": {"temp_c": w["temp_c"], "wind_speed_ms": w["wind_speed_ms"]},
        "summary": {
            "vehicles": s["vehicles"], "avg_speed_kmh": s["avg_speed_kmh"], "congestion_pct": pct(s["avg_congestion"]),
            "trips_last_hour": s["trips_1h"], "avg_trip_min": s["avg_trip_min"],
            "demand_mw": round(s["demand_mw"]), "capacity_mw": round(s["capacity_mw"]),
            "grid_load_pct": pct(s["demand_mw"] / s["capacity_mw"]),
            "avg_pm25": s["avg_pm25"], "max_pm25": s["max_pm25"], "sensor_faults": s["sensor_faults"],
            "ems_calls_last_hour": s["calls_1h"], "avg_ems_eta_min": s["avg_eta_min"],
            "active_incidents": s["incidents"], "active_alerts": len(alerts),
        },
        "busiest_major_roads": [{"road": road_label(r), "id": r["id"], "congestion_pct": pct(o["congestion"]),
                                 "speed_kmh": o["speed_kmh"], "incident": bool(o.get("incident"))} for o, r in busiest],
        "highest_grid_load": [{"district": f"{d['id']} {d['name']}", "load_pct": pct(o["utilization"]),
                               "demand_mw": round(o["demand_mw"], 1)} for o, d in dists],
        "worst_air": [{"station": stations[o["id"]]["name"], "pm25": o["pm25"]} for o in sorted(ok, key=lambda o: -o["pm25"])[:3]],
        "alerts": [{"severity": a["severity"], "title": a["title"]} for a in alerts[-8:]],
        "source": SYNTHETIC,
    }


async def district_report(gw: Gateway, district: str, **_: Any) -> dict:
    city, t = await gw.city(), await gw.telemetry()
    d = find_district(city, district)
    if d is None:
        return {"error": f"no district matches {district!r}",
                "districts": [f"{x['id']} {x['name']}" for x in city["districts"]]}
    di = d["index"]
    now_iso = t["sim_time"]
    before = await gw.telemetry_at(tick_time(t["tick"] - 3600))
    idx = [i for i, r in enumerate(city["roads"]) if r["district"] == di]
    cong_now = length_weighted(city, t["roads"], idx)
    cong_before = length_weighted(city, before["roads"], idx) if before else None
    city_now = t["summary"]["avg_congestion"]
    roads = sorted(idx, key=lambda i: -t["roads"][i]["congestion"])
    top = []
    for i in roads[:5]:
        o, r = t["roads"][i], city["roads"][i]
        top.append({"road": road_label(r), "id": r["id"], "class": r["class"], "congestion_pct": pct(o["congestion"]),
                    "speed_kmh": o["speed_kmh"], "limit_kmh": r["speed_kmh"],
                    "congestion_pct_1h_ago": pct(before["roads"][i]["congestion"]) if before else None,
                    "incident": o.get("incident") or None})
    incidents = [{"road": road_label(city["roads"][i]), "incident": t["roads"][i]["incident"]}
                 for i in idx if t["roads"][i].get("incident")]
    road_ids = {city["roads"][i]["id"] for i in idx}
    alerts = [a["title"] for a in (t.get("alerts") or []) if a["entity_id"] == d["id"] or a["entity_id"] in road_ids]
    o = t["districts"][di]
    out = {
        "sim_time": fmt_sim_time(now_iso),
        "phase": phase_of_day(now_iso),
        "district": {"id": d["id"], "name": d["name"], "type": d["type"], "population": d["population"], "jobs": d["jobs"]},
        "traffic": {"congestion_pct": pct(cong_now), "congestion_pct_1h_ago": pct(cong_before),
                    "change_pts_last_hour": pct(cong_now) - pct(cong_before) if cong_before is not None else None,
                    "city_congestion_pct": pct(city_now), "vehicles_in_district": o["vehicles"],
                    "most_congested_roads": top, "incidents": incidents},
        "energy": {"demand_mw": o["demand_mw"], "capacity_mw": o["capacity_mw"], "load_pct": pct(o["utilization"])},
        "air": {"pm25_field_mean": o["pm25"]},
        "emergency": {"calls_last_hour": o["calls_1h"], "avg_eta_min": o["avg_eta_min"]},
        "alerts": alerts[:8],
        "source": SYNTHETIC,
    }
    fc = await gw.forecasts()
    if "traffic" in fc:
        f = fc["traffic"]
        pos = {e: k for k, e in enumerate(f["entities"])}
        vals = [f["values"]["30"][pos[city["roads"][i]["id"]]] for i in idx if city["roads"][i]["id"] in pos]
        if vals:
            out["traffic"]["forecast_mean_congestion_pct_in_30_min"] = pct(sum(vals) / len(vals))
            out["traffic"]["forecast_model"] = f["version"]
    if "energy" in fc:
        f = fc["energy"]
        k = f["entities"].index(d["id"])
        series = [(h, f["values"][str(h)][k]) for h in f["horizons"]]
        h, peak = max(series, key=lambda x: x[1])
        out["energy"]["forecast_peak_next_24h_mw"] = round(peak, 1)
        out["energy"]["forecast_peak_at"] = fmt_sim_time(f["issued_tick"] + h * 3600)
        out["energy"]["forecast_model"] = f["version"]
    return out


async def road_report(gw: Gateway, road: str, **_: Any) -> dict:
    city, t = await gw.city(), await gw.telemetry()
    segs = find_roads(city, road)
    if not segs:
        return {"error": f"no road matches {road!r}", "example_names": sorted({r["name"] for r in city["roads"] if r["class"] != "local"})}
    fc = (await gw.forecasts()).get("traffic")
    pos = {e: k for k, e in enumerate(fc["entities"])} if fc else {}
    flagged = {a["entity_id"]: a["reason"] for a in ((await gw.anomalies()) or {}).get("flagged", [])}
    rows = []
    for r in segs:
        o = t["roads"][r["index"]]
        row = {"segment": r["segment"], "id": r["id"], "congestion_pct": pct(o["congestion"]), "speed_kmh": o["speed_kmh"],
               "limit_kmh": r["speed_kmh"], "flow_vph": round(o["flow_vph"]), "incident": o.get("incident") or None}
        if r["id"] in pos:
            row["forecast_congestion_pct"] = {f"+{h}min": pct(fc["values"][str(h)][pos[r["id"]]]) for h in fc["horizons"]}
        if r["id"] in flagged:
            row["anomaly_detector"] = flagged[r["id"]]
        rows.append(row)
    rows.sort(key=lambda x: -x["congestion_pct"])
    return {"sim_time": fmt_sim_time(t["sim_time"]), "road": segs[0]["name"], "class": segs[0]["class"],
            "segments": rows[:12], "segment_count": len(segs),
            "forecast_model": fc["version"] if fc else None, "source": SYNTHETIC}


async def traffic_outlook(gw: Gateway, horizon_min: int = 30, limit: int = 8, **_: Any) -> dict:
    horizon_min = min((15, 30, 60), key=lambda h: abs(h - int(horizon_min)))
    fc = (await gw.forecasts()).get("traffic")
    if not fc:
        return {"error": "the traffic model service is not running, so there is no forecast"}
    city, t = await gw.city(), await gw.telemetry()
    roads = {r["id"]: r for r in city["roads"]}
    vals = fc["values"][str(horizon_min)]
    ranked = sorted(range(len(vals)), key=lambda k: -vals[k])[: max(1, min(int(limit), 15))]
    rows = []
    for k in ranked:
        r = roads[fc["entities"][k]]
        now = t["roads"][r["index"]]["congestion"]
        rows.append({"road": road_label(r), "id": r["id"], "class": r["class"], "now_pct": pct(now),
                     "forecast_pct": pct(vals[k]), "change_pts": pct(vals[k] - now),
                     "incident": t["roads"][r["index"]].get("incident") or None})
    quality = await _model_mae(gw, "traffic")
    return {"issued": fmt_sim_time(fc["issued_tick"]), "horizon_min": horizon_min, "roads": rows,
            "model": fc["version"],
            "model_test_mae_congestion_pts": round(quality["test_mae_by_horizon"][str(horizon_min)]["model_mae"] * 100, 1) if quality else None,
            "note": "Forecast from an XGBoost model trained on other simulated cities. " + SYNTHETIC}


async def energy_outlook(gw: Gateway, window: str = "next_24h", **_: Any) -> dict:
    fc = (await gw.forecasts()).get("energy")
    if not fc:
        return {"error": "the energy model service is not running, so there is no forecast"}
    city = await gw.city()
    dists = {d["id"]: d for d in city["districts"]}
    tonight = window == "tonight"
    rows = []
    for k, did in enumerate(fc["entities"]):
        best_v, best_h = -1.0, None
        for h in fc["horizons"]:
            when = tick_time(fc["issued_tick"] + h * 3600)
            if tonight and not (18 <= when.hour <= 23):
                continue
            v = fc["values"][str(h)][k]
            if v > best_v:
                best_v, best_h = v, h
        if best_h is None:
            continue
        d = dists[did]
        rows.append({"district": f"{did} {d['name']}", "type": d["type"], "peak_mw": round(best_v, 1),
                     "peak_at": fmt_sim_time(fc["issued_tick"] + best_h * 3600), "capacity_mw": d["substation_mw"],
                     "peak_load_pct": pct(best_v / d["substation_mw"])})
    rows.sort(key=lambda x: -x["peak_mw"])
    totals = [sum(fc["values"][str(h)]) for h in fc["horizons"]]
    quality = await _model_mae(gw, "energy")
    return {"issued": fmt_sim_time(fc["issued_tick"]),
            "window": "tonight (18:00–24:00 of the next 24 h)" if tonight else "next 24 hours",
            "districts_by_peak": rows, "city_peak_mw": round(max(totals)),
            "model": fc["version"],
            "model_test_mae_mw": {h: quality["test_mae_by_horizon"][h]["model_mae"] for h in ("1", "6", "24")} if quality else None,
            "note": "Forecast from an XGBoost model; it has no weather forecast, only current and past temperature. " + SYNTHETIC}


async def air_quality_outlook(gw: Gateway, **_: Any) -> dict:
    city, t = await gw.city(), await gw.telemetry()
    fc = (await gw.forecasts()).get("pm25")
    fac = {f["id"]: f for f in city["facilities"]}
    pos = {e: k for k, e in enumerate(fc["entities"])} if fc else {}
    rows = []
    for o in t["stations"]:
        row = {"station": fac[o["id"]]["name"], "id": o["id"], "status": o["status"], "pm25_now": o["pm25"]}
        if o["id"] in pos:
            row["forecast_pm25"] = {f"+{h}min": r1(fc["values"][str(h)][pos[o["id"]]]) for h in fc["horizons"]}
        rows.append(row)
    rows.sort(key=lambda r: -(r.get("forecast_pm25", {}).get("+60min") or r["pm25_now"] or 0))
    return {"sim_time": fmt_sim_time(t["sim_time"]), "stations": rows,
            "model": fc["version"] if fc else None,
            "thresholds": "US EPA colour guide: 12 moderate, 35.5 unhealthy for sensitive groups, 55.5 unhealthy",
            "note": "PM2.5 here comes from a simple dispersion model and is not an environmental forecast. " + SYNTHETIC}


async def active_alerts(gw: Gateway, limit: int = 15, **_: Any) -> dict:
    t = await gw.telemetry()
    an = await gw.anomalies()
    alerts = (t.get("alerts") or [])[-max(1, min(int(limit), 30)):]
    return {"sim_time": fmt_sim_time(t["sim_time"]),
            "rule_alerts": [{"severity": a["severity"], "kind": a["kind"], "title": a["title"], "detail": a["detail"],
                             "since": fmt_sim_time(a["opened_at"])} for a in reversed(alerts)],
            "model_anomalies": [{"entity": a["entity_id"], "reason": a["reason"]} for a in (an or {}).get("flagged", [])[:10]],
            "model_anomaly_note": "Isolation Forest flags are a second opinion; on the benchmark the rules score higher F1." if an else
                                  "anomaly detector not running",
            "source": SYNTHETIC}


async def run_scenario(gw: Gateway, kind: str, road: str | None = None, demand_pct: float | None = None,
                       district: str | None = None, emission_factor: float | None = None,
                       temp_delta_c: float | None = None, duration_min: int = 120, **_: Any) -> dict:
    city = await gw.city()
    params: dict[str, Any] = {}
    name = None
    if kind == "road_closure":
        segs = find_roads(city, road or "")
        if not segs:
            return {"error": f"no road matches {road!r}"}
        params["road_ids"] = [r["id"] for r in segs][:24]
        name = f"Close {segs[0]['name']}" + (f" ({len(segs)} segments)" if len(segs) > 1 else f" ({segs[0]['segment']})")
    elif kind == "rush_hour":
        params["demand_pct"] = float(demand_pct or 30)
    elif kind == "signal_timing":
        params["policy"] = "adaptive"
    elif kind == "industrial_spike":
        d = find_district(city, district or "") or next(x for x in city["districts"] if x["type"] == "industrial")
        params["district_id"], params["emission_factor"] = d["id"], float(emission_factor or 4)
    elif kind == "heatwave":
        params["temp_delta_c"] = float(temp_delta_c or 6)
    else:
        return {"error": f"unknown scenario kind {kind!r}"}
    try:
        body = {"kind": kind, "params": params, "duration_min": int(duration_min)}
        if name:
            body["name"] = name
        sc = await gw.create_scenario(body)
        sc = await gw.wait_scenario(sc["id"])
    except GatewayError as e:
        return {"error": str(e)}
    if sc["status"] != "done":
        return {"scenario_id": sc["id"], "status": sc["status"], "error": sc.get("error") or "still running; check the Scenario Lab"}
    r = sc["result"]
    b, s = r["baseline"], r["scenario"]
    keys = ["avg_trip_min", "avg_congestion", "avg_speed_kmh", "trips_completed", "energy_mwh", "max_utilization",
            "avg_pm25", "alerts_opened", "rerouted", "stranded"]
    compare = {k: {"baseline": b[k], "scenario": s[k], "change": round(s[k] - b[k], 4),
                   "change_pct": round((s[k] - b[k]) / b[k] * 100, 1) if b[k] else None} for k in keys}
    roads = {x["id"]: x for x in city["roads"]}
    worst = sorted(r["roads"], key=lambda x: -x["delta"])[:5]
    out = {
        "scenario_id": sc["id"], "name": sc["name"], "window": f"{fmt_sim_time(r['start_time'])}–{fmt_sim_time(r['end_time'])[4:]}",
        "compare": compare,
        "most_worsened_roads": [{"road": road_label(roads[x["id"]]), "congestion_pct": [pct(x["baseline"]), pct(x["scenario"])],
                                 "closed": x.get("closed", False)} for x in worst],
        "spec_hash": sc["spec_hash"], "result_hash": r["result_hash"],
        "note": r["note"],
    }
    if r.get("context"):
        out["postgis_context"] = {k: v for k, v in r["context"].items() if k != "nearby_roads"}
        out["postgis_context"]["radius_km"] = r["context"].get("radius_m", 0) / 1000
        if r["context"].get("nearby_roads"):
            out["postgis_context"]["nearby_road_count"] = len(r["context"]["nearby_roads"])
    f = r.get("forecasts") or {}
    out["model_forecasts_after_window"] = {
        "traffic_horizon_min": 30, "pm25_horizon_min": 60,
        "traffic_mean_congestion_pct_30min": [pct(f["traffic"]["baseline"]["30"]), pct(f["traffic"]["scenario"]["30"])] if f.get("traffic") else None,
        "energy_mwh_next_24h": [f["energy"]["baseline_mwh_24h"], f["energy"]["scenario_mwh_24h"]] if f.get("energy") else None,
        "pm25_mean_60min": [f["pm25"]["baseline"]["60"], f["pm25"]["scenario"]["60"]] if f.get("pm25") else None,
        "unavailable": f.get("errors"),
    }
    return out


async def model_quality(gw: Gateway, model: str, **_: Any) -> dict:
    meta = await gw.model_meta(model)
    if not meta:
        return {"error": f"no {model} model is being served"}
    status = next((m for m in await gw.models() if m["kind"] == model), None)
    out: dict[str, Any] = {"model": model, "version": meta["version"], "description": meta["description"],
                           "trained_on": f"simulated cities {meta['data']['train_seeds']}, tested on later days and on unseen city {meta['data']['holdout_seeds']}",
                           "caveat": meta.get("notes")}
    if model == "anomaly":
        out["f1_test"] = {d: {"isolation_forest": round(m["test"]["model"]["f1"], 3), "rule_baseline": round(m["test"]["rule"]["f1"], 3)}
                          for d, m in meta["metrics"].items()}
    else:
        q = await _model_mae(gw, model)
        out["test_mae"] = q["test_mae_by_horizon"] if q else None
        if status and status.get("live_accuracy"):
            out["live_on_this_run"] = [{"horizon": a["horizon"], "model_mae": round(a["model_mae"], 4),
                                        "baseline_mae": None if a["baseline_mae"] is None else round(a["baseline_mae"], 4)}
                                       for a in status["live_accuracy"]][:6]
    return out


# ----------------------------------------------------------- registry

@dataclass(frozen=True)
class Tool:
    name: str
    description: str
    parameters: dict
    fn: Callable[..., Awaitable[dict]]

    def schema(self) -> dict:
        return {"type": "function", "function": {"name": self.name, "description": self.description, "parameters": self.parameters}}


def _obj(props: dict | None = None, required: list[str] | None = None) -> dict:
    return {"type": "object", "properties": props or {}, "required": required or [], "additionalProperties": False}


TOOLS: dict[str, Tool] = {t.name: t for t in [
    Tool("city_overview", "Current city-wide conditions: traffic, energy, air quality, emergency calls, busiest roads, alerts.", _obj(), city_overview),
    Tool("district_report", "Everything about one district now and one hour ago: congestion and its most congested roads, incidents, energy load and forecast, air, alerts. Accepts 'D4', 'District 4' or a name like 'Southgate'.",
         _obj({"district": {"type": "string"}}, ["district"]), district_report),
    Tool("road_report", "Live state and 15/30/60-minute congestion forecast for a road (all its segments), e.g. 'Highway A', 'Harbor Avenue', 'R102'.",
         _obj({"road": {"type": "string"}}, ["road"]), road_report),
    Tool("traffic_outlook", "Roads with the highest forecast congestion at a horizon (15, 30 or 60 minutes), with the model's measured error.",
         _obj({"horizon_min": {"type": "integer", "enum": [15, 30, 60]}, "limit": {"type": "integer"}}), traffic_outlook),
    Tool("energy_outlook", "District electricity demand forecast: peak per district over the next 24 h, or only tonight (18:00–24:00).",
         _obj({"window": {"type": "string", "enum": ["next_24h", "tonight"]}}), energy_outlook),
    Tool("air_quality_outlook", "PM2.5 at every air station now and forecast 30/60 minutes ahead.", _obj(), air_quality_outlook),
    Tool("active_alerts", "Active rule-based alerts and the anomaly detector's current flags.", _obj({"limit": {"type": "integer"}}), active_alerts),
    Tool("run_scenario", "Run a what-if simulation from the current moment and compare it with the unchanged baseline (takes a few seconds). kind: road_closure (needs road), rush_hour (demand_pct), signal_timing, industrial_spike (district, emission_factor), heatwave (temp_delta_c).",
         _obj({"kind": {"type": "string", "enum": ["road_closure", "rush_hour", "signal_timing", "industrial_spike", "heatwave"]},
               "road": {"type": "string"}, "demand_pct": {"type": "number"}, "district": {"type": "string"},
               "emission_factor": {"type": "number"}, "temp_delta_c": {"type": "number"},
               "duration_min": {"type": "integer", "minimum": 30, "maximum": 360}}, ["kind"]), run_scenario),
    Tool("model_quality", "How accurate a model is: measured error vs baselines on held-out data, and live accuracy on this run.",
         _obj({"model": {"type": "string", "enum": ["traffic", "energy", "pm25", "anomaly"]}}, ["model"]), model_quality),
]}


async def call_tool(gw: Gateway, name: str, args: dict) -> dict:
    tool = TOOLS.get(name)
    if tool is None:
        return {"error": f"unknown tool {name!r}"}
    try:
        return await tool.fn(gw, **args)
    except GatewayError as e:
        return {"error": str(e)}
    except (TypeError, KeyError, ValueError, IndexError) as e:
        return {"error": f"{name} failed: {e}"}

