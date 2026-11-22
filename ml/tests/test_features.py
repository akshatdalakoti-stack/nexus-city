"""Feature, grid and split correctness: no look-ahead, train/serve parity,
chronological splits, missing-data handling."""

from __future__ import annotations

import numpy as np
import pytest

from nexus_ml import anomaly, features
from nexus_ml.citymeta import CityMeta
from nexus_ml.config import SPLIT_DAYS, split_of_day
from nexus_ml.grid import Grid, lag, lead


def _copy(g: Grid) -> Grid:
    return Grid(
        ticks=g.ticks.copy(),
        road={k: v.copy() for k, v in g.road.items()},
        district={k: v.copy() for k, v in g.district.items()},
        station_pm=g.station_pm.copy(),
        station_raw=g.station_raw.copy(),
        station_status=g.station_status.copy(),
        city={k: v.copy() for k, v in g.city.items()},
        meta=g.meta,
    )


def _scramble_after(g: Grid, t: int, rng) -> Grid:
    """Replace every value after time index t with noise."""
    h = _copy(g)
    for d in (h.road, h.district, h.city):
        for k, v in d.items():
            v[t + 1 :] = rng.random(v[t + 1 :].shape) * 100
    h.station_pm[t + 1 :] = rng.random(h.station_pm[t + 1 :].shape) * 100
    h.station_raw[t + 1 :] = h.station_pm[t + 1 :]
    h.station_status[t + 1 :] = 0
    return h


BUILDERS = {
    "traffic": lambda g: features.traffic_features(g, 6),
    "energy": lambda g: features.energy_features(g, 6),
    "pm25": lambda g: features.pm25_features(g, 6),
    "anomaly-traffic": anomaly.traffic_feats,
    "anomaly-energy": anomaly.energy_feats,
    "anomaly-air": anomaly.air_feats,
}


@pytest.mark.parametrize("name", list(BUILDERS))
def test_features_do_not_see_the_future(small_run, name):
    g = Grid.from_dataset(small_run["dataset"])
    t = g.T - 40
    before = BUILDERS[name](g)
    after = BUILDERS[name](_scramble_after(g, t, np.random.default_rng(0)))
    for k in before:
        np.testing.assert_array_equal(before[k][: t + 1], after[k][: t + 1], err_msg=f"{name}.{k} looks ahead")


def test_live_telemetry_builds_the_same_grid_as_the_dataset(small_run):
    """Training (CSV) and serving (telemetry JSON) must see identical inputs."""
    meta = CityMeta.from_city(small_run["city"])
    a = Grid.from_dataset(small_run["dataset"])
    b = Grid.from_telemetry(small_run["telemetry"], meta)
    np.testing.assert_array_equal(a.ticks, b.ticks)
    for k in a.road:
        np.testing.assert_allclose(a.road[k], b.road[k], err_msg=k)
    for k in a.district:
        np.testing.assert_allclose(a.district[k], b.district[k], err_msg=k)
    np.testing.assert_array_equal(a.station_status, b.station_status)
    np.testing.assert_allclose(a.station_pm, b.station_pm)
    for k in a.city:
        np.testing.assert_allclose(a.city[k], b.city[k], rtol=1e-6, err_msg=k)


def test_gaps_become_missing_rows(small_run):
    meta = CityMeta.from_city(small_run["city"])
    tel = [p for i, p in enumerate(small_run["telemetry"]) if i not in (10, 11)]
    g = Grid.from_telemetry(tel, meta)
    assert g.T == len(small_run["telemetry"])
    assert np.isnan(g.road["congestion"][10]).all() and np.isnan(g.road["congestion"][11]).all()


def test_lag_and_lead():
    a = np.arange(5, dtype=np.float32)[:, None]
    np.testing.assert_array_equal(lag(a, 2)[:, 0], [np.nan, np.nan, 0, 1, 2])
    np.testing.assert_array_equal(lead(a, 2)[:, 0], [2, 3, 4, np.nan, np.nan])


def test_splits_are_chronological_and_disjoint():
    assert [split_of_day(d) for d in (0, 19, 20, 23, 24, 27)] == ["train", "train", "val", "val", "test", "test"]
    (a, b), (c, d), (e, f) = SPLIT_DAYS["train"], SPLIT_DAYS["val"], SPLIT_DAYS["test"]
    assert a < b == c < d == e < f


def test_offline_sensor_is_missing_not_zero(small_run):
    g = Grid.from_dataset(small_run["dataset"])
    off = g.station_status == 2
    stuck = g.station_status == 1
    assert np.isnan(g.station_pm[off]).all() and np.isnan(g.station_raw[off]).all()
    assert np.isnan(g.station_pm[stuck]).all()  # stuck readings are not trusted as values
    f = features.pm25_features(g, 6)
    assert np.all(f["sensor_offline"][off] == 1)
    filled = f["pm_last_valid"]
    assert not np.isnan(filled[-1]).any(), "last valid reading should carry forward"


def test_anomaly_labels_match_events(small_run):
    g = Grid.from_dataset(small_run["dataset"])
    incidents = [e for e in g.events if e["kind"] == "incident.started"]
    spans = anomaly.event_spans(g, "traffic")
    assert len(incidents) > 0
    # Each incident is one span unless two incidents on one road touch.
    assert len(spans) <= len(incidents)
    assert anomaly.labels(g, "traffic").any()
