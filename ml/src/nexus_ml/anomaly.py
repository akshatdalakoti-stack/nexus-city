"""Isolation Forest anomaly detection for roads, districts and air stations.

Unsupervised detectors trained on the training days; each alert threshold
is the one that maximises F1 on the validation days. Labels come from the
simulation's own event log (ground truth the detectors never see):

  traffic  incident active on the road           (incident.started/cleared)
  energy   injected demand surge in the district (anomaly.energy_surge.*)
  air      faulty sensor (stuck or offline)      (sensor.fault/recovered)
           or injected emission release nearby   (anomaly.emission_release.*)

Each detector is compared with a simple rule baseline whose threshold is
tuned the same way. The traffic detector does not use the simulation's
incident flag; that would be handing it the label.
"""

from __future__ import annotations

import json
import pickle
import time
import warnings
from dataclasses import dataclass
from pathlib import Path

import numpy as np
from sklearn.ensemble import IsolationForest

from . import metrics as M
from . import registry
from .config import HOLDOUT_SEEDS, MODELS_DIR, SPLIT_DAYS, STEP_S, STEPS_PER_DAY, STEPS_PER_HOUR, TRAIN_SEEDS, split_of_day
from .features import Feats
from .grid import Grid, lag, rolling_mean, rolling_std

DOMAINS = ("traffic", "energy", "air")
ENTITY = {"traffic": "road", "energy": "district", "air": "station"}
RELEASE_RADIUS_M = 3000.0
IF_PARAMS = dict(n_estimators=200, max_samples=4096, contamination="auto", random_state=42, n_jobs=-1)
TRAIN_ROWS = 200_000


# ------------------------------------------------------------- features
# Feature sets were chosen per domain on validation F1 (never on test):
# levels (speed, congestion, substation load) help traffic and energy;
# for air quality, deviation-only features did better than adding the raw
# reading level.

def traffic_feats(g: Grid) -> Feats:
    m = g.meta
    C = g.road["congestion"]
    sr = g.road["speed_kmh"] / m.road_speed[None, :]
    flow = g.road["flow_vph"]
    nbr_sr = np.nan_to_num(sr, nan=1.0) @ m.road_adj.T
    return {
        "congestion": C,
        "cong_vs_yday": C - lag(C, STEPS_PER_DAY),
        "cong_delta15": C - lag(C, 3),
        "speed_ratio": sr.astype(np.float32),
        "speed_vs_nbr": (sr - nbr_sr).astype(np.float32),
        "speed_vs_yday": (sr - lag(sr, STEPS_PER_DAY)).astype(np.float32),
        "flow_vs_yday": (np.log1p(flow) - np.log1p(lag(flow, STEPS_PER_DAY))).astype(np.float32),
    }


def energy_feats(g: Grid) -> Feats:
    base = g.meta.district_base_mw[None, :]
    d = g.district["demand_mw"] / base
    return {
        "vs_yday": (d / lag(d, STEPS_PER_DAY) - 1).astype(np.float32),
        "vs_last_hour": (d / lag(rolling_mean(d, STEPS_PER_HOUR), 1) - 1).astype(np.float32),
        "step5": (d - lag(d, 1)).astype(np.float32),
        "step15": (d - lag(d, 3)).astype(np.float32),
        "utilization": g.district["utilization"],
    }


def air_feats(g: Grid) -> Feats:
    raw = g.station_raw  # as reported: stuck sensors repeat, offline is NaN
    S = raw.shape[1]
    with warnings.catch_warnings():
        warnings.simplefilter("ignore", category=RuntimeWarning)
        others = np.stack([np.nanmedian(np.delete(raw, i, axis=1), axis=1) for i in range(S)], axis=1)
    return {
        "missing": np.isnan(raw).astype(np.float32),
        "log_vs_others": (np.log1p(raw) - np.log1p(others)).astype(np.float32),
        "delta15_rel": ((raw - lag(raw, 3)) / (lag(raw, 3) + 5)).astype(np.float32),
        "std30": rolling_std(raw, 6),
        "flat30": (rolling_std(raw, 6) < 1e-6).astype(np.float32),
    }


FEATURES = {"traffic": traffic_feats, "energy": energy_feats, "air": air_feats}

# Rule baselines: one hand-written score per domain (higher = more anomalous).
def rule_score(domain: str, f: Feats) -> np.ndarray:
    if domain == "traffic":
        return np.nan_to_num(-f["speed_vs_yday"], nan=0.0)
    if domain == "energy":
        return np.nan_to_num(f["vs_yday"], nan=0.0)
    # air: missing or flat-lined readings are certain faults; otherwise the gap to other stations
    return np.where(f["missing"] > 0, 10.0, np.where(f["flat30"] > 0, 9.0, np.nan_to_num(np.abs(f["log_vs_others"]))))


# --------------------------------------------------------------- labels

def _road_index(rid: str) -> int:
    return int(rid[1:]) - 1


def labels(g: Grid, domain: str) -> np.ndarray:
    """Boolean [T, N]: is the entity anomalous at each grid step?"""
    m = g.meta
    N = {"traffic": m.n_roads, "energy": m.n_districts, "air": m.n_stations}[domain]
    lab = np.zeros((g.T, N), dtype=bool)
    t0 = int(g.ticks[0])

    def mark(n: int, start: int, end: int) -> None:
        a = max(0, (start - t0 + STEP_S - 1) // STEP_S)
        b = min(g.T, (end - t0) // STEP_S + 1)
        lab[a:b, n] = True

    def spans(kind_start: str, kind_end: str, key):
        open_: dict = {}
        for ev in g.events:
            if ev["kind"] == kind_start:
                open_[key(ev)] = ev["tick"]
            elif ev["kind"] == kind_end and key(ev) in open_:
                yield key(ev), open_.pop(key(ev)), ev["tick"]
        for k, s in open_.items():
            yield k, s, int(g.ticks[-1])

    if domain == "traffic":
        for rid, s, e in spans("incident.started", "incident.cleared", lambda ev: ev["entity_id"]):
            mark(_road_index(rid), s, e)
    elif domain == "energy":
        did = {d: i for i, d in enumerate(m.district_ids)}
        for d, s, e in spans("anomaly.energy_surge.started", "anomaly.energy_surge.ended", lambda ev: ev["entity_id"]):
            mark(did[d], s, e)
    else:
        sid = {s: i for i, s in enumerate(m.station_ids)}
        for st, s, e in spans("sensor.fault", "sensor.recovered", lambda ev: ev["entity_id"]):
            mark(sid[st], s, e)
        did = {d: i for i, d in enumerate(m.district_ids)}
        for (d, _stack), s, e in spans("anomaly.emission_release.started", "anomaly.emission_release.ended",
                                       lambda ev: (ev["entity_id"], ev.get("data", {}).get("stack"))):
            centre = m.district_xy[did[d]]
            near = np.hypot(*(m.station_xy - centre).T) <= RELEASE_RADIUS_M
            for i in np.nonzero(near)[0]:
                mark(int(i), s, e + 15 * 60)  # the plume lingers after the release stops
    return lab


def event_spans(g: Grid, domain: str) -> list[tuple[int, int, int]]:
    """(entity, first step, last step) for each labelled episode."""
    lab = labels(g, domain)
    out = []
    for n in range(lab.shape[1]):
        col = np.concatenate([[False], lab[:, n], [False]])
        d = np.diff(col.astype(np.int8))
        for a, b in zip(np.nonzero(d == 1)[0], np.nonzero(d == -1)[0]):
            out.append((n, int(a), int(b - 1)))
    return out


# ------------------------------------------------------------- training

@dataclass
class TrainResult:
    version: str
    path: Path
    metadata: dict


def _matrix(f: Feats, names: list[str], fill: dict[str, float]) -> np.ndarray:
    cols = [np.nan_to_num(f[n], nan=fill[n]).reshape(-1) for n in names]
    return np.stack(cols, axis=1).astype(np.float32)


def _best_threshold(scores: np.ndarray, y: np.ndarray) -> float:
    qs = np.unique(np.quantile(scores, np.linspace(0.80, 0.9995, 200)))
    best, best_f1 = qs[-1], -1.0
    for q in qs:
        f1 = M.detection_report(scores >= q, y)["f1"]
        if f1 > best_f1:
            best, best_f1 = q, f1
    return float(best)


def _event_recall(flags: np.ndarray, spans: list[tuple[int, int, int]], grace: int = 3) -> dict:
    hit, delays = 0, []
    for n, a, b in spans:
        idx = np.nonzero(flags[a : b + grace + 1, n])[0]
        if len(idx):
            hit += 1
            delays.append(int(idx[0]) * 5)
    return {"events": len(spans), "detected": hit, "event_recall": hit / len(spans) if spans else float("nan"),
            "median_delay_min": float(np.median(delays)) if delays else None}


def train(data_dir: Path, models_dir: Path = MODELS_DIR, grids=None, log=print) -> TrainResult:
    t0 = time.time()
    if grids is None:
        from .forecast import load_grids

        grids = load_grids(data_dir)
    train_grids, hold_grids = grids
    rng = np.random.default_rng(42)
    detectors: dict[str, dict] = {}
    metrics: dict = {}
    for domain in DOMAINS:
        feats = {id(g): FEATURES[domain](g) for g in (*train_grids, *hold_grids)}
        names = list(next(iter(feats.values())))
        labs = {id(g): labels(g, domain) for g in (*train_grids, *hold_grids)}

        def split_mask(g: Grid, split: str) -> np.ndarray:
            # Day 0 has no "yesterday"; those features are NaN and imputed with the training median.
            return np.array([split_of_day(int(d)) == split for d in g.day_index])

        tr_rows = []
        for g in train_grids:
            mt = split_mask(g, "train")
            tr_rows.append((g, mt))
        # Imputation values and scaling statistics from training rows only.
        stacked = {n: np.concatenate([feats[id(g)][n][mt].reshape(-1) for g, mt in tr_rows]) for n in names}
        fill = {n: float(np.nanmedian(v)) if np.isfinite(v).any() else 0.0 for n, v in stacked.items()}
        mean = {n: float(np.nanmean(v)) for n, v in stacked.items()}
        std = {n: float(np.nanstd(v)) or 1.0 for n, v in stacked.items()}
        Xtr = np.concatenate([_matrix({n: feats[id(g)][n][mt] for n in names}, names, fill) for g, mt in tr_rows])
        pick = rng.choice(len(Xtr), size=min(TRAIN_ROWS, len(Xtr)), replace=False)
        forest = IsolationForest(**IF_PARAMS).fit(Xtr[pick])
        log(f"[anomaly/{domain}] fitted on {len(pick):,} of {len(Xtr):,} training rows")

        def scores_for(g: Grid, mask: np.ndarray) -> tuple[np.ndarray, np.ndarray, np.ndarray]:
            f = feats[id(g)]
            X = _matrix({n: f[n][mask] for n in names}, names, fill)
            s = -forest.score_samples(X)
            n = f[names[0]].shape[1]
            return s.reshape(-1, n), rule_score(domain, {k: v[mask] for k, v in f.items()}), labs[id(g)][mask]

        val = [scores_for(g, split_mask(g, "val")) for g in train_grids]
        thr = _best_threshold(np.concatenate([v[0].ravel() for v in val]), np.concatenate([v[2].ravel() for v in val]))
        rthr = _best_threshold(np.concatenate([v[1].ravel() for v in val]), np.concatenate([v[2].ravel() for v in val]))

        yv = np.concatenate([v[2].ravel() for v in val])
        metrics[domain] = {"val": {
            "model": M.detection_report(np.concatenate([v[0].ravel() for v in val]) >= thr, yv),
            "rule": M.detection_report(np.concatenate([v[1].ravel() for v in val]) >= rthr, yv),
        }}
        for split, gs in (("test", train_grids), ("holdout", hold_grids)):
            res = {"model": [], "rule": [], "y": [], "spans_m": [], "spans_r": []}
            ev_m = {"events": 0, "detected": 0, "delays": []}
            ev_r = {"events": 0, "detected": 0, "delays": []}
            for g in gs:
                mask = split_mask(g, "test") if split == "test" else np.ones(g.T, dtype=bool)
                s, r, y = scores_for(g, mask)
                res["model"].append(s.ravel() >= thr)
                res["rule"].append(r.ravel() >= rthr)
                res["y"].append(y.ravel())
                offset = int(np.argmax(mask))
                spans = [(n, a - offset, b - offset) for n, a, b in event_spans(g, domain) if mask[a]]
                for acc, flags in ((ev_m, s >= thr), (ev_r, r >= rthr)):
                    e = _event_recall(flags, spans)
                    acc["events"] += e["events"]
                    acc["detected"] += e["detected"]
                    if e["median_delay_min"] is not None:
                        acc["delays"].append(e["median_delay_min"])
            y = np.concatenate(res["y"])
            metrics[domain][split] = {
                "model": M.detection_report(np.concatenate(res["model"]), y),
                "rule": M.detection_report(np.concatenate(res["rule"]), y),
                "events": {
                    "model": {"events": ev_m["events"], "detected": ev_m["detected"],
                              "event_recall": ev_m["detected"] / max(ev_m["events"], 1)},
                    "rule": {"events": ev_r["events"], "detected": ev_r["detected"],
                             "event_recall": ev_r["detected"] / max(ev_r["events"], 1)},
                },
                "label_rate": float(y.mean()),
            }
        detectors[domain] = {"forest": forest, "features": names, "fill": fill, "mean": mean, "std": std,
                             "threshold": thr, "rule_threshold": rthr}
        log(f"[anomaly/{domain}] val F1 model {metrics[domain]['val']['model']['f1']:.3f} "
            f"vs rule {metrics[domain]['val']['rule']['f1']:.3f}")

    fingerprint = {"kind": "anomaly", "params": IF_PARAMS, "features": {d: v["features"] for d, v in detectors.items()},
                   "seeds": [*TRAIN_SEEDS, *HOLDOUT_SEEDS]}
    from .forecast import _manifests

    fingerprint["data"] = _manifests(data_dir)
    version = registry.make_version("anomaly", fingerprint)
    path = registry.version_dir("anomaly", version, models_dir)
    path.mkdir(parents=True, exist_ok=True)
    with open(path / "detectors.pkl", "wb") as fh:
        pickle.dump(detectors, fh)
    meta = {
        "kind": "anomaly",
        "version": version,
        "description": "Isolation Forest anomaly detection for roads, districts and air-quality stations",
        "domains": {d: {"entity": ENTITY[d], "features": v["features"], "threshold": v["threshold"],
                        "rule_threshold": v["rule_threshold"]} for d, v in detectors.items()},
        "params": IF_PARAMS,
        "metrics": metrics,
        "data": {"train_seeds": list(TRAIN_SEEDS), "holdout_seeds": list(HOLDOUT_SEEDS), "split_days": SPLIT_DAYS,
                 "manifests": _manifests(data_dir)},
        "training_seconds": round(time.time() - t0, 1),
        "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "notes": "Unsupervised detectors; only the alert threshold is tuned, on validation labels. Labels are the "
                 "simulation's injected events. Offline sensors are trivially detectable (missing values); stuck "
                 "sensors and emission releases are the informative air-quality cases.",
    }
    registry.save_metadata(path, meta)
    registry.set_current("anomaly", version, models_dir)
    log(f"[anomaly] saved {version} in {meta['training_seconds']}s")
    return TrainResult(version=version, path=path, metadata=meta)


# ------------------------------------------------------------ inference

REASONS = {
    "cong_vs_yday": "congestion vs same time yesterday",
    "cong_delta15": "15-minute congestion change",
    "speed_ratio": "speed vs limit",
    "speed_vs_nbr": "speed vs neighbouring roads",
    "speed_vs_yday": "speed vs same time yesterday",
    "flow_vs_yday": "flow vs same time yesterday",
    "congestion": "congestion level",
    "vs_yday": "demand vs same time yesterday",
    "vs_last_hour": "demand vs last hour",
    "step5": "5-minute demand jump",
    "step15": "15-minute demand jump",
    "utilization": "substation load",
    "missing": "no reading",
    "log_vs_others": "reading vs other stations",
    "delta15_rel": "15-minute change",
    "std30": "variability over 30 minutes",
    "flat30": "reading flat for 30 minutes",
    "level": "reading level",
}


class AnomalyModel:
    def __init__(self, version: str, path: Path, meta: dict):
        self.version, self.path, self.meta = version, path, meta
        with open(path / "detectors.pkl", "rb") as fh:
            self.detectors = pickle.load(fh)  # our own artifact, written by train()

    @classmethod
    def load(cls, models_dir: Path = MODELS_DIR, version: str | None = None) -> "AnomalyModel":
        version = version or registry.current_version("anomaly", models_dir)
        if not version:
            raise FileNotFoundError(f"no trained anomaly model in {models_dir} (run: nexus-ml train anomaly)")
        path = registry.version_dir("anomaly", version, models_dir)
        return cls(version, path, json.loads((path / "metadata.json").read_text()))

    history_steps = STEPS_PER_DAY + 1

    def score_last(self, g: Grid) -> dict[str, list[dict]]:
        """Score every entity at the grid's last step. Returns, per domain,
        entries sorted by score with an explanation of the biggest deviation."""
        out: dict[str, list[dict]] = {}
        ids = {"traffic": g.meta.road_ids, "energy": g.meta.district_ids, "air": g.meta.station_ids}
        for domain, det in self.detectors.items():
            f = FEATURES[domain](g)
            last = {n: f[n][-1:] for n in det["features"]}
            X = _matrix(last, det["features"], det["fill"])
            scores = -det["forest"].score_samples(X)
            entries = []
            for i, s in enumerate(scores):
                z = {n: (float(np.nan_to_num(last[n][0, i], nan=det["fill"][n])) - det["mean"][n]) / det["std"][n]
                     for n in det["features"]}
                top = max(z, key=lambda n: abs(z[n]))
                entries.append({
                    "entity_type": ENTITY[domain], "entity_id": ids[domain][i], "score": round(float(s), 4),
                    "anomalous": bool(s >= det["threshold"]),
                    "reason": f"{REASONS.get(top, top)} ({'+' if z[top] >= 0 else '−'}{abs(z[top]):.1f}σ)",
                })
            out[domain] = sorted(entries, key=lambda e: -e["score"])
        return out


# -------------------------------------------------------------- reports

def _f(v) -> str:
    return "—" if v is None or (isinstance(v, float) and np.isnan(v)) else f"{v:.3f}"


def summary_lines(meta: dict) -> list[str]:
    lines = [f"anomaly  {meta['version']}  ({meta['description']})"]
    for d, by in meta["metrics"].items():
        lines.append(f"  {d:8s} val      F1 model {_f(by['val']['model']['f1'])} vs rule {_f(by['val']['rule']['f1'])}")
        for split in ("test", "holdout"):
            e = by[split]
            lines.append(f"  {d:8s} {split:8s} F1 model {_f(e['model']['f1'])} vs rule {_f(e['rule']['f1'])} | "
                         f"precision {_f(e['model']['precision'])} recall {_f(e['model']['recall'])} | "
                         f"events caught {e['events']['model']['detected']}/{e['events']['model']['events']} "
                         f"(rule {e['events']['rule']['detected']})")
    return lines


def markdown(meta: dict) -> str:
    out = [f"# anomaly model — {meta['version']}", "", meta["description"] + ".", "", f"> {meta['notes']}", ""]
    for d, by in meta["metrics"].items():
        out += [f"## {d} ({meta['domains'][d]['entity']})", "",
                "| Split | Detector | Precision | Recall | F1 | Flag rate | Events caught |", "|---|---|---|---|---|---|---|"]
        for split in ("test", "holdout"):
            e = by[split]
            for who in ("model", "rule"):
                r, ev = e[who], e["events"][who]
                label = "Isolation Forest" if who == "model" else "Rule baseline"
                out.append(f"| {split} | {label} | {_f(r['precision'])} | {_f(r['recall'])} | {_f(r['f1'])} | "
                           f"{r['flag_rate'] * 100:.2f}% | {ev['detected']}/{ev['events']} |")
        out += ["", f"Features: {', '.join(meta['domains'][d]['features'])}", ""]
    return "\n".join(out)
