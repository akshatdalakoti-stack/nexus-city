"""Shared fixtures: a small simulated dataset (26 h, seed 7) written both as
training tables and as live-format telemetry, generated once per session."""

from __future__ import annotations

import json
import subprocess
from pathlib import Path

import pytest

from nexus_ml.cli import _headless_cmd


@pytest.fixture(scope="session")
def small_run(tmp_path_factory) -> dict:
    root = tmp_path_factory.mktemp("sim")
    ds = root / "dataset"
    tel = root / "telemetry.jsonl"
    subprocess.run(
        [*_headless_cmd(), "-seed", "7", "-hours", "26", "-quiet", "-dataset-dir", str(ds),
         "-telemetry-out", str(tel), "-telemetry-every-min", "5"],
        check=True, capture_output=True,
    )
    payloads = [json.loads(line) for line in tel.read_text().splitlines()]
    return {"dataset": ds, "telemetry": payloads, "city": json.loads((ds / "city.json").read_text())}


@pytest.fixture(scope="session")
def models_dir() -> Path:
    from nexus_ml.config import MODELS_DIR

    return MODELS_DIR
