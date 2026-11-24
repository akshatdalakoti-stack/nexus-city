"""LLM mode: an OpenAI-compatible chat model that answers by calling the
assistant's tools. Works with Gemini's OpenAI-compatible endpoint, Adaption,
or any other endpoint that supports Chat Completions with function tools.
"""

from __future__ import annotations

import json
import logging

import httpx
from openai import APIConnectionError, APIStatusError, APITimeoutError, AsyncOpenAI

from . import prom
from .config import Config
from .gateway import Gateway
from .rules import Answer, ToolCall
from .tools import TOOLS, call_tool

log = logging.getLogger("nexus_assistant.llm")

MAX_ROUNDS = 6
MAX_TOOL_CHARS = 9000

SYSTEM_PROMPT = """You are City Intelligence, the assistant in the command center of Nexus City.
Nexus City is a fictional city run by a simulation. All its data is synthetic.

Answer questions about the city using the tools. Rules:
- Use only numbers that appear in tool results. Never estimate, invent or remember a value. If the tools
  do not have the data, say so.
- Never present values as real-world observations. They are simulated; forecasts come from machine-learning
  models trained on other simulated cities, and scenario results are simulations. Say which when it matters.
- When a tool reports a model's error, mention it if a decision depends on small differences.
- For "what if" questions, run the scenario tool rather than guessing the outcome.
- State the simulated time the data refers to.
- Request every tool you need in one turn (parallel calls) rather than one at a time.
- Be concise: a short direct answer first, then a few bullet points. Use markdown.
- Explain causes only as far as the data supports them; the simulation does not record a single cause for
  congestion."""


class LLM:
    def __init__(self, cfg: Config, http_client: httpx.AsyncClient | None = None):
        self.cfg = cfg
        self.client = AsyncOpenAI(api_key=cfg.llm_api_key, base_url=cfg.llm_base_url, timeout=cfg.llm_timeout_s,
                                  max_retries=0, http_client=http_client)

    async def answer(self, gw: Gateway, question: str, history: list[dict]) -> tuple[Answer, str]:
        """Answer with the primary model, or the fallback model if the primary fails.
        Returns the answer and the model that produced it."""
        # The fallback model is the retry, so failures move on quickly. Tool
        # results are shared between attempts so a what-if scenario runs once.
        models = [self.cfg.llm_model] + ([self.cfg.llm_fallback_model] if self.cfg.llm_fallback_model else [])
        cache: dict[str, dict] = {}
        last: Exception | None = None
        for model in models:
            try:
                return await self._run(model, gw, question, history, cache), model
            except (APIStatusError, APIConnectionError, APITimeoutError) as e:
                log.warning("model %s failed: %s", model, e)
                prom.LLM_FAILURES.labels(model).inc()
                last = e
        raise RuntimeError(f"no LLM model answered ({last})")

    async def _run(self, model: str, gw: Gateway, question: str, history: list[dict], cache: dict[str, dict]) -> Answer:
        messages: list[dict] = [{"role": "system", "content": SYSTEM_PROMPT}]
        messages += [{"role": m["role"], "content": m["content"]} for m in history if m.get("role") in ("user", "assistant")]
        messages.append({"role": "user", "content": question})
        tools = [t.schema() for t in TOOLS.values()]
        extra = {"reasoning_effort": self.cfg.llm_reasoning_effort} if self.cfg.llm_reasoning_effort else {}
        calls: list[ToolCall] = []
        for round_ in range(MAX_ROUNDS):
            # On the last round, withhold the tools so the model must answer.
            last_round = round_ == MAX_ROUNDS - 1
            resp = await self.client.chat.completions.create(
                model=model, messages=messages, temperature=0.2, **extra,
                **({} if last_round else {"tools": tools, "tool_choice": "auto"}),
            )
            msg = resp.choices[0].message
            tool_calls = [tc for tc in (msg.tool_calls or []) if getattr(tc, "type", "function") == "function"]
            if not tool_calls:
                return Answer((msg.content or "").strip() or "I couldn't produce an answer.", calls)
            # Send the assistant turn back exactly as received, extra fields included:
            # Gemini's thinking models attach a thought_signature to each tool call
            # (extra_content.google) and reject a follow-up request without it.
            turn = msg.model_dump(exclude_none=True)
            messages.append({k: v for k, v in turn.items() if k in ("role", "content", "tool_calls")} | {"role": "assistant"})
            for tc in tool_calls:
                try:
                    args = json.loads(tc.function.arguments or "{}")
                    if not isinstance(args, dict):
                        raise ValueError("arguments must be an object")
                except ValueError as e:
                    result = {"error": f"invalid tool arguments: {e}"}
                    args = {}
                else:
                    key = tc.function.name + json.dumps(args, sort_keys=True)
                    if key not in cache:
                        cache[key] = await call_tool(gw, tc.function.name, args)
                    result = cache[key]
                calls.append(ToolCall(tc.function.name, args, result))
                content = json.dumps(result, ensure_ascii=False)
                if len(content) > MAX_TOOL_CHARS:
                    content = content[:MAX_TOOL_CHARS] + '..."(truncated)"'
                messages.append({"role": "tool", "tool_call_id": tc.id, "content": content})
        return Answer("I couldn't finish within the tool-call limit.", calls)
