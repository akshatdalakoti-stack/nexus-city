"""Paths, time constants and the dataset split, in one place."""

from __future__ import annotations

import os
from datetime import datetime, timedelta, timezone
from pathlib import Path

os.environ.setdefault("MLFLOW_DISABLE_AGENT_HINT", "1")

ML_ROOT = Path(__file__).resolve().parents[2]  # .../ml
REPO_ROOT = ML_ROOT.parent
DATA_DIR = Path(os.environ.get("NEXUS_DATA_DIR", ML_ROOT / "data"))
MODELS_DIR = Path(os.environ.get("NEXUS_MODELS_DIR", ML_ROOT / "models"))
REPORTS_DIR = Path(os.environ.get("NEXUS_REPORTS_DIR", ML_ROOT / "reports"))
MLRUNS_DIR = Path(os.environ.get("NEXUS_MLRUNS_DIR", ML_ROOT / "mlruns"))

# Simulated time. Tick 0 is Monday 1 June 2026, 05:30 city time.
EPOCH = datetime(2026, 6, 1, 5, 30, tzinfo=timezone.utc)
EPOCH_MINUTE_OF_DAY = 5 * 60 + 30
SAMPLE_MIN = 5  # dataset and live feature grid
STEP_S = SAMPLE_MIN * 60
STEPS_PER_HOUR = 60 // SAMPLE_MIN
STEPS_PER_DAY = 24 * STEPS_PER_HOUR

# Which simulated cities are used for what. Seed 42 is the live twin and is
# never used for training: the simulation is deterministic, so training on
# it would put the live run's own future into the training set.
LIVE_SEED = 42
TRAIN_SEEDS = (101, 102, 103)
HOLDOUT_SEEDS = (104,)
TRAIN_DAYS = 28
HOLDOUT_DAYS = 14

# Chronological split inside each training seed, in simulated days.
SPLIT_DAYS = {"train": (0, 20), "val": (20, 24), "test": (24, 28)}

# Forecast horizons.
TRAFFIC_HORIZONS_MIN = (15, 30, 60)
PM25_HORIZONS_MIN = (30, 60)
ENERGY_HORIZONS_H = tuple(range(1, 25))


def tick_to_time(tick: int) -> datetime:
    return EPOCH + timedelta(seconds=int(tick))


def split_of_day(day: int) -> str:
    for name, (a, b) in SPLIT_DAYS.items():
        if a <= day < b:
            return name
    return "unused"
