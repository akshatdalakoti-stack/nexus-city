"""Human-readable summaries of model metadata."""

from __future__ import annotations

import math


def _f(v, digits=3) -> str:
    if v is None or (isinstance(v, float) and math.isnan(v)):
        return "—"
    return f"{v:.{digits}f}"


def _pct(v) -> str:
    if v is None or (isinstance(v, float) and math.isnan(v)):
        return "—"
    return f"{v:+.1f}%"


def flat_metrics(meta: dict) -> dict[str, float]:
    out: dict[str, float] = {}
    for split, by_h in meta.get("metrics", {}).items():
        if not isinstance(by_h, dict):
            continue
        for h, e in by_h.items():
            if isinstance(e, dict) and "model" in e and isinstance(e["model"], dict):
                for k in ("mae", "rmse"):
                    v = e["model"].get(k)
                    if isinstance(v, (int, float)) and not math.isnan(v):
                        out[f"{split}_h{h}_{k}"] = float(v)
            elif isinstance(e, dict):
                for k, v in e.items():
                    if isinstance(v, (int, float)) and not (isinstance(v, float) and math.isnan(v)):
                        out[f"{split}_{h}_{k}"] = float(v)
    return out


def summary_lines(meta: dict) -> list[str]:
    if meta["kind"] == "anomaly":
        from .anomaly import summary_lines as anomaly_lines

        return anomaly_lines(meta)
    u = meta["horizon_unit"]
    lines = [f"{meta['kind']}  {meta['version']}  ({meta['description']})"]
    for split in ("test", "holdout"):
        lines.append(f"  {split:8s} horizon  model MAE  best baseline (MAE)            improvement")
        for h, e in meta["metrics"].get(split, {}).items():
            if meta["kind"] == "energy" and int(h) not in (1, 3, 6, 12, 24):
                continue
            b = e["best_baseline"]
            lines.append(f"           {h:>3}{u:<3}  {_f(e['model']['mae']):>9}  {b:<22} {_f(e[b]['mae']):>7}   {_pct(e['mae_improvement_pct'])}")
    return lines


def markdown(meta: dict) -> str:
    if meta["kind"] == "anomaly":
        from .anomaly import markdown as anomaly_md

        return anomaly_md(meta)
    u = meta["horizon_unit"]
    out = [
        f"# {meta['kind']} model — {meta['version']}",
        "",
        f"{meta['description']}. Output: {meta['output_unit']}.",
        "",
        f"> {meta['notes']}",
        "",
        "## Data",
        "",
        f"- Training seeds {meta['data']['train_seeds']} — days {meta['data']['split_days']['train']} train, "
        f"{meta['data']['split_days']['val']} validation (early stopping), {meta['data']['split_days']['test']} test.",
        f"- Holdout seed {meta['data']['holdout_seeds']}: a different city layout never seen in training.",
        "- Rows whose target falls into a later split are dropped (embargo). The live city (seed 42) is never used.",
        f"- Rows: {meta['rows']}",
        "",
    ]
    for split in ("test", "holdout"):
        out += [f"## {split.title()} results", ""]
        baselines = meta["baselines"]
        out.append("| Horizon | XGBoost MAE | " + " | ".join(f"{b} MAE" for b in baselines) + " | XGBoost RMSE | Improvement vs best baseline |")
        out.append("|---|---|" + "---|" * len(baselines) + "---|---|")
        for h, e in meta["metrics"].get(split, {}).items():
            out.append(f"| {h} {u} | {_f(e['model']['mae'])} | " + " | ".join(_f(e[b]['mae']) for b in baselines)
                       + f" | {_f(e['model']['rmse'])} | {_pct(e['mae_improvement_pct'])} ({e['best_baseline']}) |")
        if meta["kind"] == "traffic":
            out += ["", "Harder subsets (MAE): weekday rush hours, and rows where congestion changes by more than 0.1:", "",
                    "| Horizon | Subset | XGBoost | " + " | ".join(baselines) + " |", "|---|---|---|" + "---|" * len(baselines)]
            for h, e in meta["metrics"].get(split, {}).items():
                for sub in ("peak", "changing"):
                    out.append(f"| {h} {u} | {sub} | {_f(e[sub]['model'])} | " + " | ".join(_f(e[sub][b]) for b in baselines) + " |")
        out.append("")
    out += ["## Most important features (gain share)", ""]
    for key, items in meta["feature_importance"].items():
        label = "all horizons" if key == "0" else f"{key} {u}"
        out.append(f"- **{label}:** " + ", ".join(f"{i['feature']} {i['share'] * 100:.1f}%" for i in items[:8]))
    out.append("")
    return "\n".join(out)
