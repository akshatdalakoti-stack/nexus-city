"""Traffic, energy and PM2.5 forecasting: training, evaluation and inference.

Each model is described by a Spec. Training:
  1. builds features and targets from the generated datasets,
  2. splits chronologically (train / val / test days within the training
     seeds) plus a held-out seed (a different city never seen in training),
  3. drops rows whose target falls in a later split (embargo),
  4. fits baselines and an XGBoost model (early-stopped on val),
  5. reports MAE / RMSE on test and holdout for every horizon.
"""

from __future__ import annotations

import json
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable

import numpy as np
import xgboost as xgb
from sklearn.impute import SimpleImputer
from sklearn.linear_model import Ridge
from sklearn.pipeline import make_pipeline
from sklearn.preprocessing import StandardScaler

from . import features as F
from . import metrics as M
from . import registry
from .config import (
    ENERGY_HORIZONS_H,
    HOLDOUT_SEEDS,
    MODELS_DIR,
    PM25_HORIZONS_MIN,
    SAMPLE_MIN,
    SPLIT_DAYS,
    STEP_S,
    STEPS_PER_HOUR,
    TRAFFIC_HORIZONS_MIN,
    TRAIN_SEEDS,
    split_of_day,
)
from .grid import Grid

XGB_PARAMS = dict(
    n_estimators=800,
    learning_rate=0.06,
    max_depth=7,
    min_child_weight=5,
    subsample=0.8,
    colsample_bytree=0.8,
    reg_lambda=1.0,
    tree_method="hist",
    eval_metric="mae",
    early_stopping_rounds=40,
    random_state=42,
    n_jobs=-1,
)


@dataclass(frozen=True)
class Spec:
    kind: str
    entity: str  # road | district | station
    horizons: tuple[int, ...]
    unit: str  # "min" or "h"
    features: Callable[[Grid, int], F.Feats]
    target: Callable[[Grid, int], np.ndarray]
    steps: Callable[[int], int]  # horizon -> grid steps
    issue_every: int  # sample issue times every k steps for training
    stacked: bool  # one model for all horizons (horizon is a feature)
    baselines: dict[str, str]  # name -> feature that *is* the baseline forecast
    fallback: str  # baseline used where the named one is missing
    clip: tuple[float, float | None]
    output_unit: str
    description: str


def _min_steps(h: int) -> int:
    return h // SAMPLE_MIN


SPECS: dict[str, Spec] = {
    "traffic": Spec(
        kind="traffic",
        entity="road",
        horizons=TRAFFIC_HORIZONS_MIN,
        unit="min",
        features=lambda g, h: F.traffic_features(g, _min_steps(h)),
        target=lambda g, h: F.traffic_target(g, _min_steps(h)),
        steps=_min_steps,
        issue_every=3,
        stacked=False,
        baselines={"persistence": "cong", "same_time_yesterday": "cong_yday_target"},
        fallback="cong",
        clip=(0.0, 1.0),
        output_unit="congestion index (0–1)",
        description="Road congestion 15, 30 and 60 minutes ahead",
    ),
    "energy": Spec(
        kind="energy",
        entity="district",
        horizons=ENERGY_HORIZONS_H,
        unit="h",
        features=lambda g, h: F.energy_features(g, h),
        target=lambda g, h: F.energy_target(g, h),
        steps=lambda h: h * STEPS_PER_HOUR,
        issue_every=STEPS_PER_HOUR,
        stacked=True,
        baselines={"seasonal_naive_24h": "naive24_target", "seasonal_naive_7d": "naive168_target"},
        fallback="naive24_target",
        clip=(0.0, None),
        output_unit="MW",
        description="District electricity demand 1–24 hours ahead",
    ),
    "pm25": Spec(
        kind="pm25",
        entity="station",
        horizons=PM25_HORIZONS_MIN,
        unit="min",
        features=lambda g, h: F.pm25_features(g, _min_steps(h)),
        target=lambda g, h: F.pm25_target(g, _min_steps(h)),
        steps=_min_steps,
        issue_every=1,
        stacked=False,
        baselines={"persistence": "pm_last_valid"},
        fallback="pm_last_valid",
        clip=(0.0, None),
        output_unit="µg/m³ (synthetic)",
        description="Air-quality station PM2.5 30 and 60 minutes ahead",
    ),
}


# ------------------------------------------------------------- datasets

@dataclass
class Rows:
    X: np.ndarray
    y: np.ndarray
    split: np.ndarray  # train | val | test | holdout
    baselines: dict[str, np.ndarray]
    peak: np.ndarray  # weekday rush-hour rows
    now: np.ndarray  # the current value (to find rows where the value changes)
    horizon: np.ndarray
    scale: np.ndarray  # multiply predictions by this to get output units


def _issue_rows(spec: Spec, g: Grid, h: int, holdout: bool) -> Rows:
    feats = spec.features(g, h)
    names = list(feats)
    target = spec.target(g, h)
    hs = spec.steps(h)
    t_idx = np.arange(0, g.T - hs, spec.issue_every)
    issue_day = g.day_index[t_idx]
    target_day = (g.ticks[t_idx] + hs * STEP_S) // 86400
    if holdout:
        split = np.full(len(t_idx), "holdout", dtype=object)
    else:
        a = np.array([split_of_day(int(d)) for d in issue_day], dtype=object)
        b = np.array([split_of_day(int(d)) for d in target_day], dtype=object)
        split = np.where(a == b, a, "embargo")  # target crosses into a later split
    n = feats[names[0]].shape[1]
    rep = lambda v: np.repeat(v, n)  # noqa: E731
    X = F.rows(feats, names, t_idx)
    y = F.target_rows(target, t_idx)
    base = {k: F.target_rows(feats[v], t_idx) for k, v in spec.baselines.items()}
    fb = base[next(k for k, v in spec.baselines.items() if v == spec.fallback)]
    for k in base:
        base[k] = np.where(np.isnan(base[k]), fb, base[k])
    minute = g.minute_of_day[t_idx]
    peak = (g.weekday[t_idx] < 5) & (((minute >= 420) & (minute < 600)) | ((minute >= 960) & (minute < 1200)))
    now_name = {"traffic": "cong", "energy": "demand", "pm25": "pm_last_valid"}[spec.kind]
    scale = np.ones(len(y), dtype=np.float32)
    if spec.kind == "energy":
        scale = np.tile(g.meta.district_base_mw, len(t_idx)).astype(np.float32)
    return Rows(X=X, y=y, split=rep(split), baselines=base, peak=rep(peak), now=F.target_rows(feats[now_name], t_idx),
                horizon=np.full(len(y), h, dtype=np.int16), scale=scale)


def _concat(parts: list[Rows]) -> Rows:
    return Rows(
        X=np.concatenate([p.X for p in parts]),
        y=np.concatenate([p.y for p in parts]),
        split=np.concatenate([p.split for p in parts]),
        baselines={k: np.concatenate([p.baselines[k] for p in parts]) for k in parts[0].baselines},
        peak=np.concatenate([p.peak for p in parts]),
        now=np.concatenate([p.now for p in parts]),
        horizon=np.concatenate([p.horizon for p in parts]),
        scale=np.concatenate([p.scale for p in parts]),
    )


def feature_names(spec: Spec, g: Grid) -> list[str]:
    return list(spec.features(g, spec.horizons[0]))


# ------------------------------------------------------------- training

@dataclass
class TrainResult:
    version: str
    path: Path
    metadata: dict = field(default_factory=dict)


def _fit_xgb(X: np.ndarray, y: np.ndarray, Xv: np.ndarray, yv: np.ndarray) -> xgb.XGBRegressor:
    model = xgb.XGBRegressor(**XGB_PARAMS)
    model.fit(X, y, eval_set=[(Xv, yv)], verbose=False)
    return model


def _evaluate(spec: Spec, r: Rows, pred: np.ndarray, extra: dict[str, np.ndarray]) -> dict:
    """Metrics per split and horizon, in output units."""
    out: dict = {}
    lo, hi = spec.clip
    pred = np.clip(pred, lo, hi)
    for split in ("test", "holdout"):
        out[split] = {}
        for h in spec.horizons:
            m = (r.split == split) & (r.horizon == h) & ~np.isnan(r.y)
            if not m.any():
                continue
            y = r.y[m] * r.scale[m]
            entry = {"model": M.regression_report(y, pred[m] * r.scale[m])}
            for name, b in {**r.baselines, **extra}.items():
                entry[name] = M.regression_report(y, np.clip(b[m], lo, hi) * r.scale[m])
            best = min((k for k in entry if k != "model"), key=lambda k: entry[k]["mae"])
            entry["best_baseline"] = best
            entry["mae_improvement_pct"] = M.improvement(entry["model"]["mae"], entry[best]["mae"])
            if spec.kind == "traffic":
                pk = m & r.peak
                ch = m & (np.abs(r.y - r.now) > 0.1)
                entry["peak"] = {k: M.mae(r.y[pk], np.clip(v[pk], lo, hi)) for k, v in {"model": pred, **r.baselines, **extra}.items()}
                entry["changing"] = {k: M.mae(r.y[ch], np.clip(v[ch], lo, hi)) for k, v in {"model": pred, **r.baselines, **extra}.items()}
                entry["changing"]["n"] = int(ch.sum())
            if spec.kind == "energy":
                entry["model"]["mape"] = M.mape(y, pred[m] * r.scale[m])
                entry[best]["mape"] = M.mape(y, np.clip(r.baselines[best][m], lo, hi) * r.scale[m]) if best in r.baselines else None
            out[split][str(h)] = entry
    return out


def _importance(model: xgb.XGBRegressor, names: list[str], top: int = 15) -> list[dict]:
    gain = model.get_booster().get_score(importance_type="gain")
    total = sum(gain.values()) or 1.0
    items = sorted(((names[int(k[1:])], v / total) for k, v in gain.items()), key=lambda kv: -kv[1])
    return [{"feature": n, "share": round(s, 4)} for n, s in items[:top]]


def load_grids(data_dir: Path) -> tuple[list[Grid], list[Grid]]:
    train = [Grid.from_dataset(data_dir / f"seed-{s}") for s in TRAIN_SEEDS]
    hold = [Grid.from_dataset(data_dir / f"seed-{s}") for s in HOLDOUT_SEEDS]
    return train, hold


def _manifests(data_dir: Path) -> list[dict]:
    out = []
    for s in (*TRAIN_SEEDS, *HOLDOUT_SEEDS):
        m = json.loads((data_dir / f"seed-{s}" / "manifest.json").read_text())
        out.append({k: m[k] for k in ("seed", "hours", "sample_minutes", "samples", "final_state_hash", "city_version")})
    return out


def train(kind: str, data_dir: Path, models_dir: Path = MODELS_DIR, grids=None, log=print) -> TrainResult:
    spec = SPECS[kind]
    t0 = time.time()
    train_grids, hold_grids = grids or load_grids(data_dir)
    names = feature_names(spec, train_grids[0])

    def rows_for(h: int) -> Rows:
        parts = [_issue_rows(spec, g, h, False) for g in train_grids] + [_issue_rows(spec, g, h, True) for g in hold_grids]
        return _concat(parts)

    # Stacked models (energy) train once on all horizons; the others train
    # one model per horizon, built and discarded in turn to bound memory.
    groups = [(0, list(spec.horizons))] if spec.stacked else [(h, [h]) for h in spec.horizons]
    models: dict[int, xgb.XGBRegressor] = {}
    metrics: dict = {"test": {}, "holdout": {}}
    sizes: dict = {}
    for key, hs in groups:
        r = _concat([rows_for(h) for h in hs])
        ok = ~np.isnan(r.y)
        tr, va = ok & (r.split == "train"), ok & (r.split == "val")
        sizes[str(key)] = {s: int((ok & (r.split == s)).sum()) for s in ("train", "val", "test", "holdout")}
        log(f"[{kind}] h={key or 'all'}: fitting XGBoost on {tr.sum():,} rows (val {va.sum():,}) after {time.time() - t0:.0f}s")
        models[key] = _fit_xgb(r.X[tr], r.y[tr], r.X[va], r.y[va])
        pred = models[key].predict(r.X).astype(np.float32)
        extra: dict[str, np.ndarray] = {}
        if kind == "traffic":  # a linear baseline model as well as naive baselines
            idx = np.nonzero(tr)[0]
            sub = idx[:: max(1, len(idx) // 300_000)]
            ridge = make_pipeline(SimpleImputer(strategy="median"), StandardScaler(), Ridge(alpha=1.0))
            ridge.fit(r.X[sub], r.y[sub])
            extra["ridge"] = ridge.predict(r.X).astype(np.float32)
        m = _evaluate(spec, r, pred, extra)
        for split in metrics:
            metrics[split].update(m.get(split, {}))
        del r, pred, extra

    fingerprint = {"kind": kind, "features": names, "params": XGB_PARAMS, "data": _manifests(data_dir), "split": SPLIT_DAYS}
    version = registry.make_version(kind, fingerprint)
    path = registry.version_dir(kind, version, models_dir)
    path.mkdir(parents=True, exist_ok=True)
    for key, model in models.items():
        model.save_model(path / f"xgb_h{key}.json")
    meta = {
        "kind": kind,
        "version": version,
        "description": spec.description,
        "entity": spec.entity,
        "horizons": list(spec.horizons),
        "horizon_unit": spec.unit,
        "output_unit": spec.output_unit,
        "stacked": spec.stacked,
        "features": names,
        "xgb_params": XGB_PARAMS,
        "best_iteration": {str(k): int(m.best_iteration) for k, m in models.items()},
        "feature_importance": {str(k): _importance(m, names) for k, m in models.items()},
        "rows": sizes,
        "data": {"train_seeds": list(TRAIN_SEEDS), "holdout_seeds": list(HOLDOUT_SEEDS), "split_days": SPLIT_DAYS,
                 "sample_minutes": SAMPLE_MIN, "issue_every_steps": spec.issue_every, "manifests": _manifests(data_dir)},
        "baselines": list(spec.baselines) + (["ridge"] if kind == "traffic" else []),
        "metrics": metrics,
        "training_seconds": round(time.time() - t0, 1),
        "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "notes": "Trained and evaluated on synthetic Nexus City simulation data. Scores measure how well the "
                 "model learned the simulator, not real-world forecasting skill.",
    }
    registry.save_metadata(path, meta)
    registry.set_current(kind, version, models_dir)
    log(f"[{kind}] saved {version} in {meta['training_seconds']}s")
    return TrainResult(version=version, path=path, metadata=meta)


# ------------------------------------------------------------ inference

class ForecastModel:
    """A trained forecaster loaded from the registry."""

    def __init__(self, kind: str, version: str, path: Path, meta: dict):
        self.kind, self.version, self.path, self.meta = kind, version, path, meta
        self.spec = SPECS[kind]
        self.boosters: dict[int, xgb.XGBRegressor] = {}
        for f in sorted(path.glob("xgb_h*.json")):
            m = xgb.XGBRegressor()
            m.load_model(f)
            self.boosters[int(f.stem.removeprefix("xgb_h"))] = m

    @classmethod
    def load(cls, kind: str, models_dir: Path = MODELS_DIR, version: str | None = None) -> "ForecastModel":
        version = version or registry.current_version(kind, models_dir)
        if not version:
            raise FileNotFoundError(f"no trained {kind} model in {models_dir} (run: nexus-ml train {kind})")
        path = registry.version_dir(kind, version, models_dir)
        return cls(kind, version, path, json.loads((path / "metadata.json").read_text()))

    @property
    def history_steps(self) -> int:
        """Grid steps of history the features use (longest lag + 1)."""
        return {"traffic": 289, "energy": 7 * 288 + 1, "pm25": 13}[self.kind]

    def predict_last(self, g: Grid) -> dict[int, np.ndarray]:
        """Forecasts issued at the grid's last time step: horizon -> [N] values in output units."""
        lo, hi = self.spec.clip
        t = np.array([g.T - 1])
        out: dict[int, np.ndarray] = {}
        for h in self.spec.horizons:
            feats = self.spec.features(g, h)
            names = self.meta["features"]
            X = F.rows(feats, names, t)
            model = self.boosters[0 if self.spec.stacked else h]
            p = np.clip(model.predict(X), lo, hi)
            if self.kind == "energy":
                p = p * g.meta.district_base_mw
            out[h] = p.astype(np.float32)
        return out
