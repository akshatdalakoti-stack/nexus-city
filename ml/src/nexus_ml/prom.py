"""Prometheus metrics for the inference service, served on /metrics."""

from __future__ import annotations

import time

from fastapi import FastAPI, Request, Response
from prometheus_client import CONTENT_TYPE_LATEST, CollectorRegistry, Counter, Gauge, Histogram, generate_latest
from prometheus_client import GC_COLLECTOR, PLATFORM_COLLECTOR, PROCESS_COLLECTOR

REGISTRY = CollectorRegistry(auto_describe=True)
for c in (PROCESS_COLLECTOR, PLATFORM_COLLECTOR, GC_COLLECTOR):
    REGISTRY.register(c)

HTTP_REQUESTS = Counter("nexus_ml_http_requests_total", "HTTP requests by route and status code.",
                        ["route", "code"], registry=REGISTRY)
HTTP_SECONDS = Histogram("nexus_ml_http_request_duration_seconds", "HTTP request latency by route.", ["route"],
                         buckets=(.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10), registry=REGISTRY)
INFERENCE_SECONDS = Histogram("nexus_ml_inference_seconds", "Model inference time per call.", ["model", "source"],
                              buckets=(.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5), registry=REGISTRY)
PUBLISHED = Counter("nexus_ml_outputs_published_total", "Forecasts and anomaly scores written to Redis.", ["model"],
                    registry=REGISTRY)
MODEL_LOADED = Gauge("nexus_ml_model_loaded", "1 if the model artifact loaded and is being served.", ["model"],
                     registry=REGISTRY)
BUFFER_SAMPLES = Gauge("nexus_ml_buffer_samples", "5-minute telemetry samples held for feature building.",
                       registry=REGISTRY)
CONSUMING = Gauge("nexus_ml_consuming", "1 while the service follows the telemetry stream.", registry=REGISTRY)


def install(app: FastAPI) -> None:
    """Add request metrics middleware and the /metrics route."""

    @app.middleware("http")
    async def _observe(request: Request, call_next):
        t0 = time.perf_counter()
        code = 500
        try:
            response = await call_next(request)
            code = response.status_code
            return response
        finally:
            route = getattr(request.scope.get("route"), "path", "unmatched")
            if route != "/metrics":
                HTTP_REQUESTS.labels(route, str(code)).inc()
                HTTP_SECONDS.labels(route).observe(time.perf_counter() - t0)

    @app.get("/metrics", include_in_schema=False)
    def metrics() -> Response:
        return Response(generate_latest(REGISTRY), media_type=CONTENT_TYPE_LATEST)
