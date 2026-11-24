"""HTTP service: POST /chat answers a question; GET /status reports the mode."""

from __future__ import annotations

import json
import logging
import time
from contextlib import asynccontextmanager

import httpx
from fastapi import FastAPI
from pydantic import BaseModel, Field

from . import prom
from . import grounding, rules
from .config import Config, from_env
from .gateway import Gateway, GatewayError, fmt_sim_time
from .llm import LLM

log = logging.getLogger("nexus_assistant")


class Turn(BaseModel):
    role: str = Field(pattern="^(user|assistant)$")
    content: str = Field(max_length=8000)


class ChatRequest(BaseModel):
    message: str = Field(min_length=1, max_length=1000)
    history: list[Turn] = Field(default_factory=list, max_length=20)


class ToolTrace(BaseModel):
    name: str
    args: dict
    ok: bool
    result: dict


class ChatResponse(BaseModel):
    answer: str
    mode: str  # llm | rules
    model: str | None
    note: str | None = None
    sim_time: str | None
    tool_calls: list[ToolTrace]
    grounding: dict
    elapsed_ms: int


def create_app(cfg: Config | None = None, gateway_client: httpx.AsyncClient | None = None,
               llm_client: httpx.AsyncClient | None = None) -> FastAPI:
    cfg = cfg or from_env()
    state: dict = {}

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        state["gw"] = Gateway(cfg.gateway_url, client=gateway_client)
        state["llm"] = LLM(cfg, http_client=llm_client) if cfg.llm_enabled else None
        log.info("assistant ready: %s", cfg.describe())
        yield
        await state["gw"].close()

    app = FastAPI(title="Nexus City assistant", version="0.4.0", lifespan=lifespan)
    prom.install(app)

    @app.get("/health")
    def health():
        return {"status": "ok"}

    @app.get("/status")
    async def status():
        gw: Gateway = state["gw"]
        try:
            clock = await gw.clock()
            gw_ok, sim = True, fmt_sim_time(clock["sim_time"])
        except GatewayError:
            gw_ok, sim = False, None
        return {**cfg.describe(), "gateway_ok": gw_ok, "sim_time": sim}

    @app.post("/chat", response_model=ChatResponse)
    async def chat(req: ChatRequest):
        gw: Gateway = state["gw"]
        t0 = time.perf_counter()
        note, model, mode = None, None, "rules"
        try:
            if state["llm"] is not None:
                try:
                    ans, model = await state["llm"].answer(gw, req.message, [t.model_dump() for t in req.history])
                    mode = "llm"
                except RuntimeError as e:
                    note = f"The language model was unavailable ({e}); answered in rule-based mode."
                    ans = await rules.answer(gw, req.message)
            else:
                ans = await rules.answer(gw, req.message)
        except GatewayError as e:
            ans = rules.Answer(f"I can't reach the city data right now: {e}", [])
        sources = [c.result for c in ans.tool_calls] + [c.args for c in ans.tool_calls] + [req.message]
        g = grounding.check(ans.text, sources)
        if g["ungrounded"]:
            log.warning("ungrounded numbers in answer: %s", g["ungrounded"])
            prom.UNGROUNDED.inc(len(g["ungrounded"]))
        elapsed = time.perf_counter() - t0
        prom.CHATS.labels(mode, "yes" if note else "no").inc()
        prom.CHAT_SECONDS.labels(mode).observe(elapsed)
        for c in ans.tool_calls:
            prom.TOOL_CALLS.labels(c.name, "false" if "error" in c.result else "true").inc()
        # The simulated time the data refers to; tools called later in a slow
        # answer can see a later time, so report the span.
        times = [t for c in ans.tool_calls if isinstance(t := c.result.get("sim_time") or c.result.get("issued")
                                                        or c.result.get("window"), str)]
        sim_time = None
        if times:
            first, last = times[0], times[-1]
            same_day = first.split()[:-1] == last.split()[:-1]
            sim_time = first if first == last else f"{first}–{last.split()[-1] if same_day else last}"
        return ChatResponse(
            answer=ans.text, mode=mode, model=model, note=note, sim_time=sim_time,
            tool_calls=[ToolTrace(name=c.name, args=c.args, ok="error" not in c.result,
                                  result=json.loads(json.dumps(c.result))) for c in ans.tool_calls],
            grounding=g, elapsed_ms=int(elapsed * 1000))

    return app
