"""Evaluation metrics. All functions ignore rows where either side is NaN."""

from __future__ import annotations

import numpy as np


def _valid(y: np.ndarray, p: np.ndarray) -> tuple[np.ndarray, np.ndarray]:
    ok = ~(np.isnan(y) | np.isnan(p))
    return y[ok], p[ok]


def mae(y: np.ndarray, p: np.ndarray) -> float:
    y, p = _valid(y, p)
    return float(np.mean(np.abs(y - p))) if len(y) else float("nan")


def rmse(y: np.ndarray, p: np.ndarray) -> float:
    y, p = _valid(y, p)
    return float(np.sqrt(np.mean((y - p) ** 2))) if len(y) else float("nan")


def mape(y: np.ndarray, p: np.ndarray) -> float:
    """Mean absolute percentage error, only where |y| > 1e-6."""
    y, p = _valid(y, p)
    ok = np.abs(y) > 1e-6
    return float(np.mean(np.abs((y[ok] - p[ok]) / y[ok])) * 100) if ok.any() else float("nan")


def regression_report(y: np.ndarray, p: np.ndarray) -> dict[str, float]:
    return {"mae": mae(y, p), "rmse": rmse(y, p), "n": int((~(np.isnan(y) | np.isnan(p))).sum())}


def improvement(model_err: float, baseline_err: float) -> float:
    """Percentage reduction in error relative to the baseline (positive = better)."""
    if not baseline_err or np.isnan(baseline_err):
        return float("nan")
    return float((baseline_err - model_err) / baseline_err * 100)


def detection_report(flags: np.ndarray, labels: np.ndarray) -> dict[str, float]:
    """Point-wise precision/recall/F1 for boolean anomaly flags."""
    flags, labels = flags.astype(bool), labels.astype(bool)
    tp = int((flags & labels).sum())
    fp = int((flags & ~labels).sum())
    fn = int((~flags & labels).sum())
    precision = tp / (tp + fp) if tp + fp else float("nan")
    recall = tp / (tp + fn) if tp + fn else float("nan")
    f1 = 2 * precision * recall / (precision + recall) if precision and recall and not np.isnan(precision + recall) else 0.0
    return {"precision": precision, "recall": recall, "f1": f1, "tp": tp, "fp": fp, "fn": fn,
            "flag_rate": float(flags.mean()) if len(flags) else float("nan")}
