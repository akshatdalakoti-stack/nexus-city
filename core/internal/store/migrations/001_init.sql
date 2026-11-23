-- Nexus City schema, version 1.
-- Observations (what the simulation did), forecasts (what a model
-- predicted) and scenario projections (what a what-if run produced) live in
-- separate tables and are never mixed.

CREATE EXTENSION IF NOT EXISTS postgis;

-- ---------------------------------------------------------------- city
-- A city is fully determined by its seed and generator version.
CREATE TABLE cities (
    id                bigserial PRIMARY KEY,
    seed              bigint      NOT NULL,
    generator_version text        NOT NULL,
    name              text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (seed, generator_version)
);

CREATE TABLE districts (
    city_id       bigint           NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    id            text             NOT NULL,
    name          text             NOT NULL,
    type          text             NOT NULL,
    population    integer          NOT NULL,
    jobs          integer          NOT NULL,
    base_load_mw  double precision NOT NULL,
    substation_mw double precision NOT NULL,
    geom          geometry(Polygon, 4326) NOT NULL,
    PRIMARY KEY (city_id, id)
);

CREATE TABLE intersections (
    city_id    bigint  NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    id         text    NOT NULL,
    name       text    NOT NULL,
    signalized boolean NOT NULL,
    cycle_s    integer,
    green_ns_s integer,
    offset_s   integer,
    geom       geometry(Point, 4326) NOT NULL,
    PRIMARY KEY (city_id, id)
);

CREATE TABLE road_segments (
    city_id     bigint           NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    id          text             NOT NULL,
    name        text             NOT NULL,
    segment     text             NOT NULL,
    class       text             NOT NULL,
    lanes       integer          NOT NULL,
    speed_kmh   double precision NOT NULL,
    length_m    double precision NOT NULL,
    district_id text             NOT NULL,
    geom        geometry(LineString, 4326) NOT NULL,
    PRIMARY KEY (city_id, id)
);
CREATE INDEX road_segments_geom_idx ON road_segments USING gist (geom);

CREATE TABLE facilities (
    city_id     bigint  NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    id          text    NOT NULL,
    kind        text    NOT NULL,
    name        text    NOT NULL,
    district_id text    NOT NULL,
    capacity    integer,
    geom        geometry(Point, 4326) NOT NULL,
    PRIMARY KEY (city_id, id)
);

-- ---------------------------------------------------------------- runs
CREATE TABLE simulation_runs (
    run_id     text        PRIMARY KEY,
    city_id    bigint      NOT NULL REFERENCES cities (id),
    seed       bigint      NOT NULL,
    kind       text        NOT NULL CHECK (kind IN ('live', 'scenario', 'training')),
    started_at timestamptz NOT NULL DEFAULT now(),
    last_tick  bigint      NOT NULL DEFAULT 0
);

-- ------------------------------------------------------- observations
-- 5-minute samples of the live run, written by the recorder.
CREATE TABLE traffic_observations (
    run_id     text        NOT NULL REFERENCES simulation_runs (run_id) ON DELETE CASCADE,
    tick       bigint      NOT NULL,
    sim_time   timestamptz NOT NULL,
    road_id    text        NOT NULL,
    flow_vph   real        NOT NULL,
    speed_kmh  real        NOT NULL,
    congestion real        NOT NULL,
    vehicles   integer     NOT NULL,
    incident   text,
    PRIMARY KEY (run_id, tick, road_id)
);

CREATE TABLE electricity_observations (
    run_id      text        NOT NULL REFERENCES simulation_runs (run_id) ON DELETE CASCADE,
    tick        bigint      NOT NULL,
    sim_time    timestamptz NOT NULL,
    district_id text        NOT NULL,
    demand_mw   real        NOT NULL,
    utilization real        NOT NULL,
    temp_c      real        NOT NULL,
    PRIMARY KEY (run_id, tick, district_id)
);

CREATE TABLE pollution_observations (
    run_id     text        NOT NULL REFERENCES simulation_runs (run_id) ON DELETE CASCADE,
    tick       bigint      NOT NULL,
    sim_time   timestamptz NOT NULL,
    station_id text        NOT NULL,
    pm25       real, -- NULL when the sensor is offline
    status     text        NOT NULL,
    PRIMARY KEY (run_id, tick, station_id)
);

CREATE TABLE simulation_events (
    id          bigserial   PRIMARY KEY,
    run_id      text        NOT NULL REFERENCES simulation_runs (run_id) ON DELETE CASCADE,
    tick        bigint      NOT NULL,
    sim_time    timestamptz NOT NULL,
    kind        text        NOT NULL,
    severity    text        NOT NULL,
    entity_type text        NOT NULL,
    entity_id   text        NOT NULL,
    message     text        NOT NULL,
    data        jsonb
);
CREATE INDEX simulation_events_run_tick_idx ON simulation_events (run_id, tick);

-- ------------------------------------------------------ model outputs
CREATE TABLE model_versions (
    version     text        PRIMARY KEY,
    kind        text        NOT NULL,
    description text,
    first_seen  timestamptz NOT NULL DEFAULT now(),
    last_seen   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE forecasts (
    run_id        text    NOT NULL REFERENCES simulation_runs (run_id) ON DELETE CASCADE,
    model_version text    NOT NULL REFERENCES model_versions (version),
    issued_tick   bigint  NOT NULL,
    target_tick   bigint  NOT NULL,
    horizon       integer NOT NULL,
    entity_type   text    NOT NULL,
    entity_id     text    NOT NULL,
    value         real    NOT NULL,
    PRIMARY KEY (run_id, model_version, issued_tick, horizon, entity_id)
);

-- ------------------------------------------------------------ what-if
CREATE TABLE scenario_configs (
    id           text        PRIMARY KEY,
    name         text        NOT NULL,
    kind         text        NOT NULL,
    params       jsonb       NOT NULL,
    seed         bigint      NOT NULL,
    base_run_id  text, -- the live run it branched from, if any
    base_tick    bigint      NOT NULL,
    duration_min integer     NOT NULL,
    spec_hash    text        NOT NULL,
    status       text        NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'failed')),
    progress     real        NOT NULL DEFAULT 0,
    phase        text,
    error        text,
    worker       text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    started_at   timestamptz,
    finished_at  timestamptz
);
CREATE INDEX scenario_configs_created_idx ON scenario_configs (created_at DESC);

CREATE TABLE scenario_results (
    scenario_id text        PRIMARY KEY REFERENCES scenario_configs (id) ON DELETE CASCADE,
    result_hash text        NOT NULL,
    result      jsonb       NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
