"""Model artifacts on disk.

    models/<kind>/<version>/metadata.json   features, horizons, data, metrics, versions
    models/<kind>/<version>/*.json|*.pkl    the fitted estimators
    models/<kind>/CURRENT                    the version services load

Versions look like `traffic-20261119.1402-3fa9c1`: training time plus a
hash of the training data and configuration, so two trainings on the same
data with the same settings share the hash suffix.
"""

from __future__ import annotations

import hashlib
import json
import platform
from datetime import datetime, timezone
from pathlib import Path

from .config import MODELS_DIR

KINDS = ("traffic", "energy", "pm25", "anomaly")


def make_version(kind: str, fingerprint: dict) -> str:
    digest = hashlib.sha256(json.dumps(fingerprint, sort_keys=True, default=str).encode()).hexdigest()[:6]
    return f"{kind}-{datetime.now(timezone.utc):%Y%m%d.%H%M}-{digest}"


def version_dir(kind: str, version: str, root: Path = MODELS_DIR) -> Path:
    return root / kind / version


def save_metadata(path: Path, meta: dict) -> None:
    import numpy
    import sklearn
    import xgboost

    meta = {
        **meta,
        "library_versions": {
            "python": platform.python_version(),
            "numpy": numpy.__version__,
            "scikit-learn": sklearn.__version__,
            "xgboost": xgboost.__version__,
        },
    }
    path.mkdir(parents=True, exist_ok=True)
    (path / "metadata.json").write_text(json.dumps(meta, indent=2, default=_json_default))


def _json_default(o):
    import numpy as np

    if isinstance(o, (np.floating, np.integer)):
        return o.item()
    if isinstance(o, np.ndarray):
        return o.tolist()
    if isinstance(o, float) and o != o:
        return None
    return str(o)


def set_current(kind: str, version: str, root: Path = MODELS_DIR) -> None:
    (root / kind).mkdir(parents=True, exist_ok=True)
    (root / kind / "CURRENT").write_text(version + "\n")


def current_version(kind: str, root: Path = MODELS_DIR) -> str | None:
    p = root / kind / "CURRENT"
    return p.read_text().strip() if p.exists() else None


def load_metadata(kind: str, version: str | None = None, root: Path = MODELS_DIR) -> dict | None:
    version = version or current_version(kind, root)
    if not version:
        return None
    p = version_dir(kind, version, root) / "metadata.json"
    return json.loads(p.read_text()) if p.exists() else None


def list_versions(kind: str, root: Path = MODELS_DIR) -> list[str]:
    d = root / kind
    return sorted(p.name for p in d.iterdir() if p.is_dir()) if d.exists() else []
