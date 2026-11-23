"""Configuration from environment variables. No key is ever hard-coded.

LLM mode needs an OpenAI-compatible endpoint:
  LLM_BASE_URL   default: Google's Gemini OpenAI-compatible endpoint
  LLM_API_KEY    falls back to GEMINI_API_KEY if that is set
  LLM_MODEL      default gemini-flash-lite-latest (in testing it answered in ~15 s;
                 gemini-3.5-flash often took 40-100 s or timed out on the free tier)
  LLM_FALLBACK_MODEL  default gemini-3.5-flash (tried if the first fails; "none" disables)
  LLM_REASONING_EFFORT  sent as reasoning_effort. Unset or "auto": "low" on Gemini's
                 endpoint (long thinking makes tool loops slow), not sent elsewhere.
                 "off" never sends it.
Empty values count as unset, so Compose can pass variables through.
Without a key the assistant runs in rule-based mode. ASSISTANT_MODE=rules
forces rule-based mode even when a key is present.
"""

from __future__ import annotations

import os
from dataclasses import dataclass

GEMINI_OPENAI_URL = "https://generativelanguage.googleapis.com/v1beta/openai/"


@dataclass(frozen=True)
class Config:
    gateway_url: str
    llm_base_url: str
    llm_api_key: str | None
    llm_model: str
    llm_fallback_model: str | None
    llm_timeout_s: float
    mode: str  # "llm" or "rules"
    llm_reasoning_effort: str | None = None

    @property
    def llm_enabled(self) -> bool:
        return self.mode == "llm" and bool(self.llm_api_key)

    def describe(self) -> dict:
        return {"mode": "llm" if self.llm_enabled else "rules",
                "model": self.llm_model if self.llm_enabled else None,
                "fallback_model": self.llm_fallback_model if self.llm_enabled else None,
                "endpoint": self.llm_base_url if self.llm_enabled else None}


def _env(name: str, default: str) -> str:
    return os.environ.get(name, "").strip() or default


def from_env() -> Config:
    base_url = _env("LLM_BASE_URL", GEMINI_OPENAI_URL)
    effort = _env("LLM_REASONING_EFFORT", "auto").lower()
    if effort == "auto":
        effort = "low" if "generativelanguage.googleapis.com" in base_url else ""
    elif effort == "off":
        effort = ""
    key = _env("LLM_API_KEY", "") or _env("GEMINI_API_KEY", "") or None
    mode = _env("ASSISTANT_MODE", "auto").lower()
    fallback = _env("LLM_FALLBACK_MODEL", "gemini-3.5-flash")
    return Config(
        gateway_url=_env("GATEWAY_URL", "http://localhost:8080"),
        llm_base_url=base_url,
        llm_api_key=key,
        llm_model=_env("LLM_MODEL", "gemini-flash-lite-latest"),
        llm_fallback_model=None if fallback.lower() == "none" else fallback,
        llm_timeout_s=float(_env("LLM_TIMEOUT_S", "45")),
        mode="rules" if mode == "rules" or not key else "llm",
        llm_reasoning_effort=effort or None,
    )
