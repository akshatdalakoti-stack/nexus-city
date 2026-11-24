"""Rule-based mode: answers without any LLM.

A small intent matcher picks the right tool, and templates turn its JSON
into prose. Every number in an answer is copied from a tool result, so this
mode cannot invent a measurement. It answers the brief's example questions:

  - Why is District 4 experiencing congestion?
  - Which district is likely to consume the most electricity tonight?
  - What happens if we close Highway A?
  - Which roads need attention in the next 30 minutes?
  - Summarize the city's current condition.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field

from .gateway import Gateway
from .resolve import find_district, find_roads
from .tools import call_tool


@dataclass
class ToolCall:
    name: str
    args: dict
    result: dict


@dataclass
class Answer:
    text: str
    tool_calls: list[ToolCall] = field(default_factory=list)


class Recorder:
    """Calls tools and keeps the trace shown under each answer."""

    def __init__(self, gw: Gateway):
        self.gw, self.calls = gw, []

    async def __call__(self, name: str, **args) -> dict:
        res = await call_tool(self.gw, name, args)
        self.calls.append(ToolCall(name, args, res))
        return res


def _has(q: str, *words: str) -> bool:
    return any(re.search(rf"\b{w}", q) for w in words)


def _num(q: str, pattern: str, default: float | None = None) -> float | None:
    m = re.search(pattern, q)
    return float(m.group(1)) if m else default


def _signed(x: float, unit: str = "", digits: int = 1) -> str:
    return f"{'+' if x >= 0 else '−'}{abs(x):.{digits}f}{unit}"


HELP = """I can answer questions about the simulated city from live data, for example:

- *Summarize the city's current condition.*
- *Why is District 4 experiencing congestion?*
- *Which district is likely to consume the most electricity tonight?*
- *Which roads need attention in the next 30 minutes?*
- *What happens if we close Highway A?* (runs a what-if simulation)
- *How is Harbor Avenue doing?* · *Any alerts?* · *How's the air quality?* · *How accurate is the energy model?*"""


async def answer(gw: Gateway, question: str) -> Answer:
    q = question.lower().strip()
    rec = Recorder(gw)
    city = await gw.city()

    # What-if scenarios.
    if _has(q, "what if", "what happens", "what would happen", "simulat", "if we ", "suppose") or re.search(r"\b(close|closing|shut)\b", q):
        kind = None
        if re.search(r"\b(close|closing|closure|shut|block)", q) and find_roads(city, q):
            kind = "road_closure"
        elif _has(q, "heat", "hotter", "temperature"):
            kind = "heatwave"
        elif _has(q, "signal", "traffic light", "adaptive"):
            kind = "signal_timing"
        elif _has(q, "industr", "factory", "factories", "emission"):
            kind = "industrial_spike"
        elif _has(q, "rush", "more traffic", "more trips", "demand", "surge"):
            kind = "rush_hour"
        if kind:
            hours = _num(q, r"(\d+(?:\.\d+)?)\s*(?:h|hours?)\b")
            args: dict = {"kind": kind, "duration_min": int(min(max((hours or 2) * 60, 30), 360))}
            if kind == "road_closure":
                args["road"] = q
            elif kind == "heatwave":
                args["temp_delta_c"] = _num(q, r"(\d+(?:\.\d+)?)\s*(?:°|degrees?|c\b)", 6)
            elif kind == "rush_hour":
                args["demand_pct"] = _num(q, r"(\d+(?:\.\d+)?)\s*%", 30)
            elif kind == "industrial_spike":
                d = find_district(city, q)
                if d:
                    args["district"] = d["id"]
                args["emission_factor"] = _num(q, r"(?:x|×|times)\s*(\d+(?:\.\d+)?)|(\d+(?:\.\d+)?)\s*(?:x|×|times)", 4) or 4
            return Answer(_scenario_text(await rec("run_scenario", **args)), rec.calls)

    district = find_district(city, q)
    if district and _has(q, "why", "cause", "reason", "explain") and _has(q, "congest", "traffic", "jam", "busy", "slow"):
        return Answer(_why_congested(await rec("district_report", district=district["id"])), rec.calls)
    if _has(q, "electric", "energy", "power", "consum", "grid", "substation", "demand"):
        window = "tonight" if _has(q, "tonight", "evening", "night") else "next_24h"
        return Answer(_energy_text(await rec("energy_outlook", window=window)), rec.calls)
    if district:
        return Answer(_district_text(await rec("district_report", district=district["id"])), rec.calls)
    if _has(q, "air", "pm2", "pollut", "smog"):
        return Answer(_air_text(await rec("air_quality_outlook")), rec.calls)
    if _has(q, "accura", "reliab", "trust", "how good", "error", "model"):
        kind = next((k for k, w in [("traffic", "traffic"), ("energy", "energy"), ("energy", "electric"), ("pm25", "air"),
                                     ("pm25", "pm2"), ("anomaly", "anomal")] if w in q), "traffic")
        return Answer(_quality_text(await rec("model_quality", model=kind)), rec.calls)
    if _has(q, "alert", "anomal", "incident", "problem", "wrong", "issue"):
        return Answer(_alerts_text(await rec("active_alerts")), rec.calls)
    roads = find_roads(city, q)
    if roads and not _has(q, "which roads", "what roads"):
        return Answer(_road_text(await rec("road_report", road=roads[0]["name"])), rec.calls)
    if _has(q, "road", "traffic", "congest", "attention", "next \\d+", "forecast", "jam"):
        h = int(_num(q, r"(\d+)\s*(?:min|minutes)", 30))
        if _has(q, "hour") and not _has(q, "half"):
            h = 60
        return Answer(_traffic_text(await rec("traffic_outlook", horizon_min=h, limit=6)), rec.calls)
    if _has(q, "summar", "overview", "status", "condition", "how is the city", "what's happening", "whats happening", "how are things"):
        return Answer(_overview_text(await rec("city_overview")), rec.calls)
    return Answer(HELP, [])


# ------------------------------------------------------------- templates

def _err(res: dict) -> str | None:
    return f"I couldn't get that data: {res['error']}" if "error" in res else None


def _overview_text(o: dict) -> str:
    if e := _err(o):
        return e
    s = o["summary"]
    lines = [f"**Nexus City at {o['sim_time']}** · {o['phase']} · {o['weather']['temp_c']} °C", "",
             f"- **Traffic:** {s['vehicles']:,} vehicles, average speed {s['avg_speed_kmh']} km/h, congestion index {s['congestion_pct']}%; "
             f"{s['trips_last_hour']:,} trips finished in the last hour (average {s['avg_trip_min']} min).",
             f"- **Electricity:** {s['demand_mw']} MW of {s['capacity_mw']} MW substation capacity ({s['grid_load_pct']}%).",
             f"- **Air:** station average PM2.5 {s['avg_pm25']} µg/m³, highest {s['max_pm25']}"
             + (f"; {s['sensor_faults']} sensor(s) faulty." if s["sensor_faults"] else "."),
             f"- **Emergency:** {s['ems_calls_last_hour']} calls in the last hour"
             + (f", average response {s['avg_ems_eta_min']} min." if s["ems_calls_last_hour"] else "."),
             f"- **Incidents / alerts:** {s['active_incidents']} incident(s), {s['active_alerts']} active alert(s)."]
    if o["busiest_major_roads"]:
        top = o["busiest_major_roads"][:3]
        lines += ["", "Busiest major roads: " + "; ".join(f"{r['road']} {r['congestion_pct']}% ({r['speed_kmh']} km/h)" for r in top) + "."]
    if o["highest_grid_load"]:
        d = o["highest_grid_load"][0]
        lines.append(f"Highest grid load: {d['district']} at {d['load_pct']}%.")
    return "\n".join(lines)


def _why_congested(r: dict) -> str:
    if e := _err(r):
        return e
    d, tr = r["district"], r["traffic"]
    now, before, city = tr["congestion_pct"], tr["congestion_pct_1h_ago"], tr["city_congestion_pct"]
    head = f"**{d['id']} {d['name']}** ({d['type']}) at {r['sim_time']}: congestion index **{now}%** across its roads, vs {city}% city-wide"
    head += f" and {before}% an hour ago." if before is not None else "."
    lines = [head, ""]
    if now <= max(city, 10) and (before is None or now <= before + 3):
        lines.append(f"It isn't unusually congested right now: it's at or below the city average. What the data shows:")
    else:
        lines.append("What the data shows alongside it:")
    phase = r["phase"]
    if "rush" in phase:
        if d["type"] in ("residential", "mixed"):
            flow = "commuters leave for work" if "morning" in phase else "commuters return home"
        else:
            flow = "commuters arrive for work" if "morning" in phase else "commuters leave work"
        lines.append(f"- **Time of day:** it's the {phase}. {d['name']} has {d['population']:,} residents and {d['jobs']:,} jobs, "
                     f"so this is when {flow}.")
    else:
        lines.append(f"- **Time of day:** {phase}, not a commuter peak.")
    change = tr["change_pts_last_hour"]
    if change is not None and abs(change) >= 3:
        lines.append(f"- **Trend:** congestion {'rose' if change > 0 else 'fell'} {abs(change)} points in the last hour.")
    for inc in tr["incidents"]:
        lines.append(f"- **Incident:** {inc['incident']} on {inc['road']} is closing lanes.")
    worst = [x for x in tr["most_congested_roads"] if x["congestion_pct"] >= 20][:3]
    for x in worst:
        ago = f", was {x['congestion_pct_1h_ago']}% an hour ago" if x["congestion_pct_1h_ago"] is not None else ""
        lines.append(f"- **{x['road']}** ({x['class']}): {x['congestion_pct']}% congested, {x['speed_kmh']} of {x['limit_kmh']} km/h{ago}.")
    if not worst and tr["most_congested_roads"]:
        x = tr["most_congested_roads"][0]
        lines.append(f"- Its busiest road is {x['road']} at only {x['congestion_pct']}%.")
    if "forecast_mean_congestion_pct_in_30_min" in tr:
        lines.append(f"- **Next 30 min:** the traffic model forecasts {tr['forecast_mean_congestion_pct_in_30_min']}% "
                     f"average congestion on these roads.")
    if r["alerts"]:
        lines.append(f"- **Alerts:** " + "; ".join(r["alerts"][:3]) + ".")
    lines += ["", "_The simulation doesn't record a single cause for congestion; these are the conditions present at the same time._"]
    return "\n".join(lines)


def _district_text(r: dict) -> str:
    if e := _err(r):
        return e
    d, tr, en = r["district"], r["traffic"], r["energy"]
    lines = [f"**{d['id']} {d['name']}** ({d['type']}, {d['population']:,} residents, {d['jobs']:,} jobs) at {r['sim_time']}:", "",
             f"- **Traffic:** congestion {tr['congestion_pct']}% (city {tr['city_congestion_pct']}%), {tr['vehicles_in_district']:,} vehicles in the district.",
             f"- **Electricity:** {en['demand_mw']} MW of {en['capacity_mw']} MW ({en['load_pct']}%)"
             + (f"; forecast peak {en['forecast_peak_next_24h_mw']} MW at {en['forecast_peak_at']}." if "forecast_peak_next_24h_mw" in en else "."),
             f"- **Air:** PM2.5 {r['air']['pm25_field_mean']} µg/m³ (district mean of the modelled field).",
             f"- **Emergency:** {r['emergency']['calls_last_hour']} calls in the last hour"
             + (f", average response {r['emergency']['avg_eta_min']} min." if r["emergency"]["calls_last_hour"] else ".")]
    if r["alerts"]:
        lines.append("- **Alerts:** " + "; ".join(r["alerts"][:3]) + ".")
    return "\n".join(lines)


def _energy_text(o: dict) -> str:
    if e := _err(o):
        return e
    rows = o["districts_by_peak"]
    if not rows:
        return f"The energy forecast issued {o['issued']} doesn't cover {o['window']}."
    top = rows[0]
    lines = [f"**{top['district']}** is forecast to use the most electricity {('tonight' if 'tonight' in o['window'] else 'in the next 24 hours')}: "
             f"a peak of **{top['peak_mw']} MW** around {top['peak_at']}, {top['peak_load_pct']}% of its {top['capacity_mw']} MW substation.", "",
             "Next highest: " + "; ".join(f"{r['district']} {r['peak_mw']} MW ({r['peak_load_pct']}%)" for r in rows[1:4]) + "."]
    tight = [r for r in rows if r["peak_load_pct"] >= 90]
    if tight:
        lines.append("Close to capacity: " + ", ".join(f"{r['district']} ({r['peak_load_pct']}%)" for r in tight) + ".")
    mae = o.get("model_test_mae_mw") or {}
    lines += ["", f"_Forecast issued {o['issued']} by {o['model']}"
              + (f"; typical error on held-out days ±{mae.get('6', 0):.1f} MW at 6 h, ±{mae.get('24', 0):.1f} MW at 24 h" if mae else "")
              + ". It has no weather forecast, only current and past temperature._"]
    return "\n".join(lines)


def _traffic_text(o: dict) -> str:
    if e := _err(o):
        return e
    h = o["horizon_min"]
    lines = [f"Roads the traffic model expects to be most congested **{h} minutes** after {o['issued']}:", ""]
    for r in o["roads"]:
        inc = f" — incident {r['incident']}" if r["incident"] else ""
        lines.append(f"- **{r['road']}**: {r['now_pct']}% now → **{r['forecast_pct']}%** forecast ({_signed(r['change_pts'], ' pts', 0)}){inc}")
    if o.get("model_test_mae_congestion_pts") is not None:
        lines += ["", f"_{o['model']}: average error on held-out days is about {o['model_test_mae_congestion_pts']} congestion points at this horizon, "
                      f"so treat small differences between roads as noise._"]
    return "\n".join(lines)


def _air_text(o: dict) -> str:
    if e := _err(o):
        return e
    rows = o["stations"]
    lines = [f"Air quality at {o['sim_time']} (PM2.5, µg/m³):", ""]
    for s in rows[:5]:
        fc = s.get("forecast_pm25", {})
        now = "offline" if s["pm25_now"] is None else s["pm25_now"]
        extra = f" → {fc.get('+60min')} in 60 min" if fc.get("+60min") is not None else ""
        flag = f" ({s['status']} sensor)" if s["status"] != "ok" else ""
        lines.append(f"- {s['station']}: {now}{extra}{flag}")
    lines += ["", f"_{o['thresholds']}. {o['note']}_"]
    return "\n".join(lines)


def _alerts_text(o: dict) -> str:
    if e := _err(o):
        return e
    ra, ma = o["rule_alerts"], o["model_anomalies"]
    if not ra and not ma:
        return f"No active alerts or model anomalies at {o['sim_time']}."
    lines = [f"**{len(ra)} active alert(s)** at {o['sim_time']}:", ""]
    lines += [f"- [{a['severity']}] {a['title']}: {a['detail']} (since {a['since']})" for a in ra[:8]]
    if ma:
        lines += ["", f"The anomaly detector also flags: " + "; ".join(f"{a['entity']} ({a['reason']})" for a in ma[:6]) + ".",
                  f"_{o['model_anomaly_note']}_"]
    return "\n".join(lines)


def _road_text(o: dict) -> str:
    if e := _err(o):
        return e
    segs = o["segments"]
    lines = [f"**{o['road']}** ({o['class']}, {o['segment_count']} segment(s)) at {o['sim_time']}:", ""]
    for s in segs[:6]:
        fc = s.get("forecast_congestion_pct")
        f = f"; forecast {fc['+30min']}% in 30 min" if fc else ""
        inc = f"; incident {s['incident']}" if s["incident"] else ""
        an = f"; anomaly detector: {s['anomaly_detector']}" if s.get("anomaly_detector") else ""
        lines.append(f"- {s['segment']}: {s['congestion_pct']}% congested, {s['speed_kmh']} of {s['limit_kmh']} km/h{f}{inc}{an}")
    if o["segment_count"] > 6:
        lines.append(f"- …and {o['segment_count'] - 6} more segment(s), less congested.")
    return "\n".join(lines)


def _quality_text(o: dict) -> str:
    if e := _err(o):
        return e
    lines = [f"**{o['description']}** ({o['version']}), trained on {o['trained_on']}.", ""]
    if "f1_test" in o:
        for d, f in o["f1_test"].items():
            lines.append(f"- {d}: Isolation Forest F1 {f['isolation_forest']} vs rule baseline {f['rule_baseline']} on test days.")
    else:
        for h, m in (o.get("test_mae") or {}).items():
            if o["model"] == "energy" and h not in ("1", "6", "24"):
                continue
            lines.append(f"- {h} {'h' if o['model'] == 'energy' else 'min'} ahead: error {m['model_mae']} vs {m['baseline_mae']} for "
                         f"{m['best_baseline'].replace('_', ' ')} ({_signed(m['improvement_pct'], '%')} better).")
    lines += ["", f"_{o['caveat']}_"]
    return "\n".join(lines)


def _scenario_text(o: dict) -> str:
    if e := _err(o):
        return e
    c = o["compare"]

    def row(label: str, key: str, fmt=lambda v: f"{v}", unit: str = "") -> str:
        b, s = c[key]["baseline"], c[key]["scenario"]
        rel = f" ({_signed(c[key]['change_pct'], '%')})" if c[key]["change_pct"] is not None else ""
        return f"- {label}: {fmt(b)} → **{fmt(s)}**{unit}{rel}"

    pct_fmt = lambda v: f"{round(v * 100, 1)}%"  # noqa: E731
    lines = [f"Simulated **{o['name']}**, {o['window']}, compared with the unchanged city:", "",
             row("Average journey", "avg_trip_min", unit=" min"),
             row("Congestion index", "avg_congestion", pct_fmt),
             row("Network speed", "avg_speed_kmh", unit=" km/h"),
             row("Electricity used", "energy_mwh", unit=" MWh"),
             row("Peak substation load", "max_utilization", pct_fmt),
             row("PM2.5 (city mean)", "avg_pm25", unit=" µg/m³")]
    if c["rerouted"]["scenario"]:
        lines.append(f"- Vehicles re-routed mid-trip: {c['rerouted']['scenario']:,}"
                     + (f"; stranded with no route: {c['stranded']['scenario']:,}" if c["stranded"]["scenario"] else ""))
    worst = [w for w in o["most_worsened_roads"] if not w["closed"]][:3]
    if worst:
        lines += ["", "Most worsened roads: " + "; ".join(f"{w['road']} {w['congestion_pct'][0]}% → {w['congestion_pct'][1]}%" for w in worst) + "."]
    ctx = o.get("postgis_context") or {}
    if "nearby_road_count" in ctx:
        lines.append(f"Roads within {ctx['radius_km']:g} km of the closure: {ctx['nearby_road_count']}, "
                     f"congestion {_signed(ctx.get('nearby_mean_delta', 0) * 100, ' pts')} on average.")
    mf = o.get("model_forecasts_after_window") or {}
    if mf.get("traffic_mean_congestion_pct_30min"):
        b, s = mf["traffic_mean_congestion_pct_30min"]
        lines.append(f"After the window, the traffic model forecasts {b}% → {s}% mean congestion 30 min ahead.")
    lines += ["", f"_{o['note']} Scenario {o['scenario_id']}, result {o['result_hash']} — open it in the Scenario Lab for charts and the map._"]
    return "\n".join(lines)
