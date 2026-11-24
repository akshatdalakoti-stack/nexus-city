"""Command line:

    nexus-assistant serve [--host 127.0.0.1] [--port 8095]
    nexus-assistant ask "Which roads need attention in the next 30 minutes?"
"""

from __future__ import annotations

import argparse
import asyncio
import logging
import os


def main(argv: list[str] | None = None) -> None:
    ap = argparse.ArgumentParser(prog="nexus-assistant")
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("serve")
    s.add_argument("--host", default=os.environ.get("ASSISTANT_HOST", "127.0.0.1"),
                   help="bind address (containers use 0.0.0.0)")
    s.add_argument("--port", type=int, default=8095)
    a = sub.add_parser("ask")
    a.add_argument("question")
    a.add_argument("--rules", action="store_true", help="force rule-based mode")
    args = ap.parse_args(argv)
    logging.basicConfig(level=logging.INFO)

    if args.cmd == "serve":
        import uvicorn

        from .service import create_app

        uvicorn.run(create_app(), host=args.host, port=args.port)
        return

    from dataclasses import replace

    from . import grounding, rules
    from .config import from_env
    from .gateway import Gateway
    from .llm import LLM

    async def run() -> None:
        cfg = from_env()
        if args.rules:
            cfg = replace(cfg, mode="rules")
        gw = Gateway(cfg.gateway_url)
        try:
            if cfg.llm_enabled:
                ans, model = await LLM(cfg).answer(gw, args.question, [])
                print(f"[llm: {model}]")
            else:
                ans = await rules.answer(gw, args.question)
                print("[rule-based mode]")
            print(ans.text)
            print("\ntools:", ", ".join(f"{c.name}({c.args})" for c in ans.tool_calls) or "none")
            g = grounding.check(ans.text, [c.result for c in ans.tool_calls] + [c.args for c in ans.tool_calls] + [args.question])
            print(f"grounding: {g['checked']} numbers checked, ungrounded: {g['ungrounded'] or 'none'}")
        finally:
            await gw.close()

    asyncio.run(run())


if __name__ == "__main__":
    main()
