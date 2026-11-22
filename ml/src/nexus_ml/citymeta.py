"""Static, per-entity attributes derived from a city JSON (as served by
GET /api/city or written to a dataset's city.json)."""

from __future__ import annotations

from dataclasses import dataclass

import networkx as nx
import numpy as np

CLASS_CODE = {"local": 0, "arterial": 1, "highway": 2}
DTYPE_CODE = {"residential": 0, "commercial": 1, "industrial": 2, "mixed": 3}


@dataclass
class CityMeta:
    seed: int
    road_ids: list[str]
    road_class: np.ndarray  # [R]
    road_lanes: np.ndarray
    road_speed: np.ndarray  # free-flow km/h
    road_length: np.ndarray  # m
    road_district: np.ndarray  # [R] district index
    road_dtype: np.ndarray  # [R] district type code
    road_adj: np.ndarray  # [R, R] row-normalised adjacency (roads sharing an intersection)
    district_ids: list[str]
    district_dtype: np.ndarray  # [D]
    district_base_mw: np.ndarray
    district_capacity_mw: np.ndarray
    district_pop: np.ndarray
    district_jobs: np.ndarray
    district_density: np.ndarray
    district_xy: np.ndarray  # [D, 2] centre, metres
    station_ids: list[str]
    station_district: np.ndarray  # [S]
    station_xy: np.ndarray  # [S, 2]
    industrial_xy: np.ndarray  # [K, 2] industrial district centres
    node_ids: list[str]  # signalised intersections, in telemetry order
    graph: nx.Graph  # road network, nodes = intersection index

    @classmethod
    def from_city(cls, city: dict) -> "CityMeta":
        roads, nodes, districts = city["roads"], city["nodes"], city["districts"]
        R = len(roads)
        g = nx.Graph()
        for r in roads:
            g.add_edge(r["a"], r["b"], road=r["index"], length=r["length_m"])

        # Roads are adjacent when they share an intersection.
        by_node: dict[int, list[int]] = {}
        for r in roads:
            by_node.setdefault(r["a"], []).append(r["index"])
            by_node.setdefault(r["b"], []).append(r["index"])
        adj = np.zeros((R, R), dtype=np.float32)
        for rs in by_node.values():
            for i in rs:
                for j in rs:
                    if i != j:
                        adj[i, j] = 1.0
        adj /= np.maximum(adj.sum(axis=1, keepdims=True), 1.0)

        dtype = np.array([DTYPE_CODE[d["type"]] for d in districts], dtype=np.int8)
        road_district = np.array([r["district"] for r in roads], dtype=np.int16)
        stations = [f for f in city["facilities"] if f["kind"] == "aq_station"]
        dxy = np.array([[(d["x0"] + d["x1"]) / 2, (d["y0"] + d["y1"]) / 2] for d in districts], dtype=np.float32)
        return cls(
            seed=int(city["seed"]),
            road_ids=[r["id"] for r in roads],
            road_class=np.array([CLASS_CODE[r["class"]] for r in roads], dtype=np.int8),
            road_lanes=np.array([r["lanes"] for r in roads], dtype=np.float32),
            road_speed=np.array([r["speed_kmh"] for r in roads], dtype=np.float32),
            road_length=np.array([r["length_m"] for r in roads], dtype=np.float32),
            road_district=road_district,
            road_dtype=dtype[road_district],
            road_adj=adj,
            district_ids=[d["id"] for d in districts],
            district_dtype=dtype,
            district_base_mw=np.array([d["base_load_mw"] for d in districts], dtype=np.float32),
            district_capacity_mw=np.array([d["substation_mw"] for d in districts], dtype=np.float32),
            district_pop=np.array([d["population"] for d in districts], dtype=np.float32),
            district_jobs=np.array([d["jobs"] for d in districts], dtype=np.float32),
            district_density=np.array([d["building_density"] for d in districts], dtype=np.float32),
            district_xy=dxy,
            station_ids=[f["id"] for f in stations],
            station_district=np.array([f["district"] for f in stations], dtype=np.int16),
            station_xy=np.array([[f["x"], f["y"]] for f in stations], dtype=np.float32),
            industrial_xy=dxy[dtype == DTYPE_CODE["industrial"]],
            node_ids=[n["id"] for n in nodes if n["signalized"]],
            graph=g,
        )

    @property
    def n_roads(self) -> int:
        return len(self.road_ids)

    @property
    def n_districts(self) -> int:
        return len(self.district_ids)

    @property
    def n_stations(self) -> int:
        return len(self.station_ids)
