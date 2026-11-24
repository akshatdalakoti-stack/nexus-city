"""Prometheus metrics for the assistant, served on /metrics."""

from __future__ import annotations

from fastapi import FastAPI, Response
from prometheus_client import CONTENT_TYPE_LATEST, CollectorRegistry, Counter, Histogram, generate_latest
from prometheus_client import GC_COLLECTOR, PLATFORM_COLLECTOR, PROCESS_COLLECTOR

REGISTRY = CollectorRegistry(auto_describe=True)
for c in (PROCESS_COLLECTOR, PLATFORM_COLLECTOR, GC_COLLECTOR):
    REGISTRY.register(c)

CHATS = Counter("nexus_assistant_chats_total", "Answered questions by mode (llm or rules) and whether the LLM fell back.",
                ["mode", "fallback"], registry=REGISTRY)
CHAT_SECONDS = Histogram("nexus_assistant_chat_duration_seconds", "Time to answer a question.", ["mode"],
                         buckets=(.25, .5, 1, 2.5, 5, 10, 20, 30, 45, 60, 90, 120, 180), registry=REGISTRY)
TOOL_CALLS = Counter("nexus_assistant_tool_calls_total", "Tool calls by tool and outcome.", ["tool", "ok"],
                     registry=REGISTRY)
UNGROUNDED = Counter("nexus_assistant_ungrounded_numbers_total",
                     "Numbers in answers that matched no tool data (flagged to the user).", registry=REGISTRY)
LLM_FAILURES = Counter("nexus_assistant_llm_failures_total", "LLM requests that failed, by model.", ["model"],
                       registry=REGISTRY)


def install(app: FastAPI) -> None:
    @app.get("/metrics", include_in_schema=False)
    def metrics() -> Response:
        return Response(generate_latest(REGISTRY), media_type=CONTENT_TYPE_LATEST)
