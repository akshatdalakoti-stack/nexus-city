"""Check that every number in an answer can be traced to the data.

Numbers in the answer are compared with numbers in the tool results, the
question and the tool arguments, allowing for rounding and for a fraction
written as a percentage (0.261 -> 26%). Identifiers (D4, R102, INC-3),
clock times (17:30), model versions and hashes are ignored, as are small
integers that are usually counts or list positions.
"""

from __future__ import annotations

import json
import math
import re
from typing import Any, Iterable

_IGNORE = [
    re.compile(r"\b[\w]+-\d{8}\.\d{4}-[0-9a-f]{6}\b"),  # model versions
    re.compile(r"\b[0-9a-f]{16}\b"),  # result / spec hashes
    re.compile(r"\bsc-[0-9a-f]{12}\b"),  # scenario ids
    re.compile(r"\b\d{1,2}:\d{2}\b"),  # clock times
    re.compile(r"\b\d{4}-\d{2}-\d{2}\b"),  # dates
    re.compile(r"\b[A-Za-z]{1,4}-?\d+\b"),  # D4, R102, INC-3, AQ1, SUB2, H1
    re.compile(r"PM2\.5", re.IGNORECASE),
    re.compile(r"\+\d+\s*(?:min|h)\b"),  # horizons like +30 min
]
_NUM = re.compile(r"(?<![\w.])[-−+]?(?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d+)?")


def numbers_in_text(text: str) -> list[tuple[str, float]]:
    for pat in _IGNORE:
        text = pat.sub(" ", text)
    out = []
    for m in _NUM.finditer(text):
        raw = m.group(0)
        try:
            out.append((raw, float(raw.replace(",", "").replace("−", "-"))))
        except ValueError:
            continue
    return out


def _walk(x: Any) -> Iterable[float]:
    if isinstance(x, bool):
        return
    if isinstance(x, (int, float)):
        if math.isfinite(x):
            yield float(x)
    elif isinstance(x, str):
        for _, v in numbers_in_text(x):
            yield v
    elif isinstance(x, dict):
        for v in x.values():
            yield from _walk(v)
    elif isinstance(x, (list, tuple)):
        for v in x:
            yield from _walk(v)


def _close(x: float, a: float) -> bool:
    if abs(x - a) <= max(0.051, 0.005 * abs(a)):
        return True
    # Rounded to a whole number or to one decimal place.
    return (x == round(x) and abs(x - a) < 0.5 + 1e-9) or abs(x - round(a, 1)) < 1e-9


def check(answer: str, sources: list[Any]) -> dict:
    """Return {'checked': n, 'ungrounded': [numbers as written]}."""
    allowed: set[float] = set()
    for s in sources:
        for v in _walk(json.loads(s) if isinstance(s, str) and s[:1] in "{[" else s):
            allowed.update({v, abs(v), v * 100, abs(v * 100)})
    checked, ungrounded = 0, []
    for raw, x in numbers_in_text(answer):
        ax = abs(x)
        if ax == round(ax) and ax <= 10:
            continue  # small counts, list positions, "top 3"
        checked += 1
        if not any(_close(ax, a) for a in allowed):
            ungrounded.append(raw)
    return {"checked": checked, "ungrounded": ungrounded}
