"""Command line: generate data, train, report, serve.

    nexus-ml generate                 # simulate the training and holdout cities
    nexus-ml train all                # traffic, energy, pm25, anomaly
    nexus-ml train traffic
    nexus-ml report                   # print current model metrics
    nexus-ml serve --models traffic,energy,pm25,anomaly
"""

from __future__ import annotations

import argparse
import json
import shutil
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

from . import registry
from .config import DATA_DIR, HOLDOUT_DAYS, HOLDOUT_SEEDS, MODELS_DIR, REPO_ROOT, TRAIN_DAYS, TRAIN_SEEDS


def _headless_cmd() -> list[str]:
    exe = REPO_ROOT / ".bin" / ("headless.exe" if sys.platform == "win32" else "headless")
    if exe.exists():
        return [str(exe)]
    if shutil.which("go"):
        return ["go", "run", "-C", str(REPO_ROOT / "core"), "./cmd/headless"]
    raise SystemExit("Need the Go toolchain or a built headless binary in .bin/ to generate data")


def generate(data_dir: Path, seeds_days: list[tuple[int, int]]) -> None:
    """Run the deterministic Go simulation for each seed (in parallel)."""
    data_dir.mkdir(parents=True, exist_ok=True)
    cmd = _headless_cmd()

    def run(sd: tuple[int, int]) -> str:
        seed, days = sd
        out = data_dir / f"seed-{seed}"
        if (out / "manifest.json").exists():
            return f"seed {seed}: exists, skipped"
        res = subprocess.run([*cmd, "-seed", str(seed), "-days", str(days), "-quiet", "-dataset-dir", str(out)],
                             capture_output=True, text=True)
        if res.returncode != 0:
            raise RuntimeError(f"seed {seed} failed: {res.stderr}")
        return f"seed {seed}: {res.stderr.strip()}"

    with ThreadPoolExecutor(max_workers=4) as pool:
        for line in pool.map(run, seeds_days):
            print(line)


def main(argv: list[str] | None = None) -> None:
    ap = argparse.ArgumentParser(prog="nexus-ml")
    sub = ap.add_subparsers(dest="cmd", required=True)
    g = sub.add_parser("generate", help="simulate training data")
    g.add_argument("--data-dir", type=Path, default=DATA_DIR)
    t = sub.add_parser("train", help="train models")
    t.add_argument("kind", choices=["traffic", "energy", "pm25", "anomaly", "all"])
    t.add_argument("--data-dir", type=Path, default=DATA_DIR)
    t.add_argument("--models-dir", type=Path, default=MODELS_DIR)
    t.add_argument("--no-mlflow", action="store_true", help="skip MLflow experiment tracking")
    r = sub.add_parser("report", help="print metrics of the current models")
    r.add_argument("--models-dir", type=Path, default=MODELS_DIR)
    s = sub.add_parser("serve", help="run inference services")
    s.add_argument("--models", default="traffic,energy,pm25,anomaly")
    s.add_argument("--port", type=int, default=8090)
    args = ap.parse_args(argv)

    if args.cmd == "generate":
        generate(args.data_dir, [(s, TRAIN_DAYS) for s in TRAIN_SEEDS] + [(s, HOLDOUT_DAYS) for s in HOLDOUT_SEEDS])
    elif args.cmd == "train":
        from .training import train_all

        kinds = ["traffic", "energy", "pm25", "anomaly"] if args.kind == "all" else [args.kind]
        train_all(kinds, args.data_dir, args.models_dir, mlflow=not args.no_mlflow)
    elif args.cmd == "report":
        for kind in registry.KINDS:
            meta = registry.load_metadata(kind, root=args.models_dir)
            if not meta:
                print(f"{kind}: not trained")
                continue
            from .reports import summary_lines

            print("\n".join(summary_lines(meta)))
    elif args.cmd == "serve":
        import uvicorn

        from .service import create_app

        uvicorn.run(create_app([m.strip() for m in args.models.split(",") if m.strip()]), host="0.0.0.0", port=args.port)


if __name__ == "__main__":
    main()
