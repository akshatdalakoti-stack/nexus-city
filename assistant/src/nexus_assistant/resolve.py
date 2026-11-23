"""Turn names people use ("District 4", "Southgate", "Highway A", "Elm
Road") into the ids the simulation uses."""

from __future__ import annotations

import re


def _norm(s: str) -> str:
    return re.sub(r"[^a-z0-9 ]+", " ", s.lower()).strip()


def find_district(city: dict, text: str) -> dict | None:
    """Match 'D4', 'district 4', 'district four' or a district name."""
    t = _norm(text)
    words = {"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8,
             "nine": 9, "ten": 10, "eleven": 11, "twelve": 12}
    m = re.search(r"\bd(?:istrict)?\s*(\d{1,2}|" + "|".join(words) + r")\b", t)
    if m:
        raw = m.group(1)
        n = int(raw) if raw.isdigit() else words[raw]
        for d in city["districts"]:
            if d["id"] == f"D{n}":
                return d
    for d in sorted(city["districts"], key=lambda d: -len(d["name"])):
        if _norm(d["name"]) in t:
            return d
    return None


def find_roads(city: dict, text: str) -> list[dict]:
    """All segments of a named road ('Highway A' -> 12 segments), a single
    segment by id ('R102'), or [] if nothing matches."""
    t = _norm(text)
    by_id = {r["id"].lower(): r for r in city["roads"]}
    ids = [by_id[m] for m in re.findall(r"\br\d{1,3}\b", t) if m in by_id]
    if ids:
        return ids
    t = re.sub(r"\bhwy\b", "highway", t)
    # Longest names first, so "Northern Avenue" wins over a shorter overlap.
    for name in sorted({r["name"] for r in city["roads"]}, key=len, reverse=True):
        if re.search(rf"\b{re.escape(_norm(name))}\b", t):
            return [r for r in city["roads"] if r["name"] == name]
    return []


def find_station(city: dict, text: str) -> dict | None:
    t = _norm(text)
    for f in city["facilities"]:
        if f["kind"] == "aq_station" and (f["id"].lower() in t.split() or _norm(f["name"]) in t):
            return f
    return None


def district_of_road(city: dict, road: dict) -> dict:
    return city["districts"][road["district"]]
