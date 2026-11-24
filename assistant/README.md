# City Intelligence (`nexus_assistant`)

Answers questions about Nexus City from live data, model forecasts and real what-if runs. It
never states a number that the tools didn't return, and it checks this on every answer.

It is a client of the gateway's public REST API and holds no state of its own. The gateway
proxies `/api/assistant/{status,chat}` to it, and the web app's **City Intelligence** page is the
user interface.

## Run

```powershell
uv sync
$env:GATEWAY_URL = "http://localhost:8080"
uv run nexus-assistant serve          # http://127.0.0.1:8095
```

To ask a single question from the terminal, run:

```powershell
uv run nexus-assistant ask "Which roads need attention in the next 30 minutes?"
```

Add `--rules` to force rule-based mode. Start the gateway with
`ASSISTANT_URL=http://localhost:8095` so the web page can reach the assistant.

## Modes

| Mode | When | How answers are made |
|---|---|---|
| `llm` | `LLM_API_KEY` or `GEMINI_API_KEY` is set | An OpenAI-compatible chat model calls the tools (up to 6 rounds), then writes the answer |
| `rules` | No key, `ASSISTANT_MODE=rules`, or every model failed | Intent matching picks tools; templates write the answer |

### LLM settings

All settings come from environment variables. Empty values count as unset.

| Variable | Default |
|---|---|
| `LLM_BASE_URL` | `https://generativelanguage.googleapis.com/v1beta/openai/` (Gemini) |
| `LLM_MODEL` | `gemini-flash-lite-latest` |
| `LLM_FALLBACK_MODEL` | `gemini-3.5-flash`. `none` disables the fallback |
| `LLM_REASONING_EFFORT` | `low` on Gemini, not sent to other providers. `off` never sends it |
| `LLM_TIMEOUT_S` | `45` per request. On a timeout, the fallback model takes over |
| `ASSISTANT_HOST` | `127.0.0.1`. The container uses `0.0.0.0` |

Any endpoint that supports Chat Completions with function tools works.

The defaults come from testing on the Gemini free tier:

- **`gemini-flash-lite-latest`** answered in about 10–20 s.
- **`gemini-3.5-flash`** often took 40–100 s, or timed out, once tool results were in the
  conversation.

Gemini's thinking models attach a `thought_signature` to each tool call. The assistant sends it
back unchanged, as Gemini requires.

When the primary model fails partway through an answer, the fallback model starts again but reuses
the tool results already fetched, so a what-if scenario is never run twice.

## Tools

| Tool | Answers |
|---|---|
| `city_overview` | "Summarize the city's current condition." |
| `district_report(district)` | "Why is District 4 experiencing congestion?" Gives congestion now and an hour ago, the worst roads, incidents, energy, air and EMS. |
| `road_report(road)` | "How is Harbor Avenue doing?" Gives every segment, with forecasts. |
| `traffic_outlook(horizon_min, limit)` | "Which roads need attention in the next 30 minutes?" |
| `energy_outlook(window)` | "Which district will use the most electricity tonight?" |
| `air_quality_outlook` | PM2.5 now and in an hour, plus faulty sensors. |
| `active_alerts(limit)` | Open alerts and anomaly flags. |
| `run_scenario(kind, …)` | "What happens if we close Highway A?" Queues a real scenario and waits up to 90 s for it. |
| `model_quality(model)` | Test error against baselines, plus live accuracy. |

Names are resolved inside the tools:

- `District 4`, `district four`, `Southgate` and `D4` all mean the same district.
- `Highway A` or `hwy a` means all 12 of its segments.
- `R102` means that one road segment.

Every result includes the simulated time and its source. The source is `simulation`,
`forecast:<model version>` or `scenario:<id>`.

## Grounding

`grounding.check(answer, sources)` extracts every number from the answer and looks for it in the
tool results, the tool arguments and the question.

- **Matches:** rounding to the precision written, fractions as percentages (0.261 → 26%), and
  absolute values of changes.
- **Ignored:** times, dates, IDs (D4, R102), model versions, scenario ids and small counting
  numbers.
- **Flagged:** anything else is returned in `grounding.ungrounded`, logged, and shown as a warning
  in the UI.

This check catches invented numbers, but it can't judge the reasoning behind a sentence.

## API

`GET /health` returns `{"status": "ok"}`.

`GET /status` returns the mode, the model and fallback model, whether the gateway is reachable,
and the current simulated time.

`POST /chat` takes this request:

```json
{"message": "Which roads need attention?", "history": [{"role": "user", "content": "..."}]}
```

It returns:

```json
{"answer": "markdown", "mode": "llm", "model": "gemini-flash-lite-latest", "note": null,
 "sim_time": "Mon 08:32", "tool_calls": [{"name": "traffic_outlook", "args": {}, "ok": true, "result": {}}],
 "grounding": {"checked": 10, "ungrounded": []}, "elapsed_ms": 14200}
```

Requests are validated:

- `message` is 1–1000 characters.
- `history` holds at most 20 turns, each with the role `user` or `assistant`. A `system` role is
  rejected.

## Tests

```powershell
uv run pytest
```

The fake gateway in `tests/conftest.py` serves real responses that were captured from a running
system into `tests/fixtures/`. Expected values are computed independently from those files.
Recapture them with `uv run python tests/capture_fixtures.py` while the stack runs.

LLM mode is tested against a scripted OpenAI-compatible endpoint, so the tests need no key and no
network. They cover:

- the tool loop;
- invalid tool arguments;
- the fallback model;
- thought-signature echo;
- reusing tool results across models;
- answering in rules mode when the LLM is down;
- flagging invented numbers.

## Limitations

- The simulation records no causes, so "why" answers describe what the data shows.
- Answers depend on the provider and model. The free tier is rate-limited and sometimes slow.
- Rules mode understands a fixed set of question shapes. Anything else gets a list of what it can
  answer.
- Conversations are not stored.
