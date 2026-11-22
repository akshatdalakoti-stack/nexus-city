"""Train a set of models, write reports and (optionally) log to MLflow."""

from __future__ import annotations

import time
from pathlib import Path

from . import forecast, reports
from .config import MLRUNS_DIR, REPORTS_DIR


def train_all(kinds: list[str], data_dir: Path, models_dir: Path, mlflow: bool = True) -> dict[str, dict]:
    grids = forecast.load_grids(data_dir)  # loaded once, shared by every model
    results: dict[str, dict] = {}
    for kind in kinds:
        t0 = time.time()
        if kind == "anomaly":
            from . import anomaly

            res = anomaly.train(data_dir, models_dir, grids=grids)
        else:
            res = forecast.train(kind, data_dir, models_dir, grids=grids)
        REPORTS_DIR.mkdir(parents=True, exist_ok=True)
        report = REPORTS_DIR / f"{kind}.md"
        report.write_text(reports.markdown(res.metadata))
        print("\n".join(reports.summary_lines(res.metadata)))
        print(f"report: {report}  ({time.time() - t0:.0f}s)\n")
        if mlflow:
            _log_mlflow(res.metadata, res.path, report)
        results[kind] = res.metadata
    return results


def _log_mlflow(meta: dict, path: Path, report: Path) -> None:
    """Experiment tracking is optional: install with `uv sync --extra tracking`."""
    # A local file store needs no server or database. MLflow 3 keeps it in
    # maintenance mode and requires this explicit opt-in.
    import os

    os.environ.setdefault("MLFLOW_ALLOW_FILE_STORE", "true")
    try:
        import mlflow
    except ImportError:
        print("mlflow not installed; skipping experiment tracking")
        return
    try:
        mlflow.set_tracking_uri(MLRUNS_DIR.resolve().as_uri())
        mlflow.set_experiment(f"nexus-{meta['kind']}")
        with mlflow.start_run(run_name=meta["version"]):
            mlflow.set_tags({"version": meta["version"], "kind": meta["kind"], "synthetic_data": "true"})
            n_features = len(meta["features"]) if "features" in meta else sum(len(d["features"]) for d in meta["domains"].values())
            mlflow.log_params({"features": n_features, **meta.get("xgb_params", meta.get("params", {}))})
            for k, v in reports.flat_metrics(meta).items():
                mlflow.log_metric(k, v)
            mlflow.log_artifact(str(path / "metadata.json"))
            mlflow.log_artifact(str(report))
    except Exception as e:  # tracking must never break training
        print(f"mlflow logging skipped: {e}")
