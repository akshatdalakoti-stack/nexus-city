"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { motion } from "motion/react";
import {
  AlertTriangle,
  ArrowUp,
  Bot,
  CheckCircle2,
  ChevronRight,
  Cpu,
  Info,
  Loader2,
  MessageSquareText,
  RotateCcw,
  Wrench,
  XCircle,
} from "lucide-react";
import { api } from "@/lib/api";
import type { AssistantStatus, ChatResponse, ChatTurn, ToolTrace } from "@/lib/types";
import { cn } from "@/lib/utils";

const SUGGESTED = [
  "Summarize the city's current condition.",
  "Why is District 4 experiencing congestion?",
  "Which roads need attention in the next 30 minutes?",
  "Which district is likely to consume the most electricity tonight?",
  "What happens if we close Highway A?",
  "How accurate is the traffic model?",
  "Are there any active alerts?",
  "What if a heatwave adds 6 °C?",
];

const MAX_MESSAGE = 1000;
const MAX_HISTORY = 20;
const WHAT_IF = /\b(what if|what happens|close|closure|heatwave|surge|adaptive|spike)\b/i;

type Message =
  | { id: number; role: "user"; text: string }
  | { id: number; role: "assistant"; resp: ChatResponse }
  | { id: number; role: "error"; text: string };

let nextId = 1;

// ------------------------------------------------------------------ pieces

function ModeBadge({ status, error }: { status: AssistantStatus | null; error: string | null }) {
  if (error) {
    return (
      <span className="flex items-center gap-1.5 rounded-md border border-crit/40 bg-crit/10 px-2 py-1 text-[12px] font-medium text-crit">
        <XCircle className="size-3" /> assistant offline
      </span>
    );
  }
  if (!status) return <span className="text-[12px] font-medium text-muted-foreground">connecting…</span>;
  return status.mode === "llm" ? (
    <span className="flex items-center gap-1.5 rounded-md border border-border bg-raised px-2 py-1 text-[12px] font-medium text-foreground" title={`Fallback: ${status.fallback_model ?? "none"}`}>
      <Cpu className="size-3" /> LLM · {status.model}
    </span>
  ) : (
    <span className="flex items-center gap-1.5 rounded-md border border-border bg-raised px-2 py-1 text-[12px] font-medium text-muted-foreground" title="No LLM key configured: answers come from deterministic templates over the same tools.">
      <Wrench className="size-3" /> rule-based
    </span>
  );
}

function Grounding({ g, mode }: { g: ChatResponse["grounding"]; mode: ChatResponse["mode"] }) {
  if (g.ungrounded.length > 0) {
    return (
      <div className="flex items-start gap-2 rounded-md border border-warn/40 bg-warn/10 px-2.5 py-1.5 text-[12px] text-warn">
        <AlertTriangle className="mt-px size-3.5 shrink-0" />
        <span>
          {g.ungrounded.length === 1 ? "This number does" : "These numbers do"} not appear in any tool result:{" "}
          <span className="font-mono">{g.ungrounded.join(", ")}</span>. Treat {g.ungrounded.length === 1 ? "it" : "them"} as unverified.
        </span>
      </div>
    );
  }
  if (g.checked === 0) return null;
  return (
    <span className="flex items-center gap-1 text-[11.5px] text-ok/90" title={mode === "llm" ? "Every number in the answer was found in the data the tools returned." : "Rule-based answers are built directly from tool data."}>
      <CheckCircle2 className="size-3" /> {g.checked} numbers checked against tool data
    </span>
  );
}

function argsText(args: Record<string, unknown>) {
  const parts = Object.entries(args).map(([k, v]) => `${k}=${typeof v === "string" ? v : JSON.stringify(v)}`);
  return parts.join(", ");
}

function ToolCallRow({ t }: { t: ToolTrace }) {
  const [open, setOpen] = useState(false);
  const json = JSON.stringify(t.result, null, 2);
  return (
    <li className="rounded-md border border-border bg-raised">
      <button type="button" onClick={() => setOpen((o) => !o)} className="flex w-full items-center gap-2 px-2 py-1.5 text-left font-mono text-[12px]" aria-expanded={open}>
        <ChevronRight className={cn("size-3 shrink-0 text-muted-foreground transition", open && "rotate-90")} />
        <span className={t.ok ? "text-brand" : "text-crit"}>{t.name}</span>
        <span className="truncate text-muted-foreground">({argsText(t.args)})</span>
        {!t.ok && <span className="ml-auto shrink-0 text-crit">error</span>}
      </button>
      {open && (
        <pre className="max-h-72 overflow-auto border-t border-border px-2.5 py-2 font-mono text-[11.5px] leading-relaxed text-muted-foreground">
          {json.length > 20000 ? json.slice(0, 20000) + "\n…" : json}
        </pre>
      )}
    </li>
  );
}

function AssistantMessage({ resp }: { resp: ChatResponse }) {
  const [trace, setTrace] = useState(false);
  return (
    <div className="flex gap-3">
      <div className="mt-0.5 grid size-7 shrink-0 place-items-center rounded-lg border border-border bg-raised">
        <Bot className="size-4 text-foreground" />
      </div>
      <div className="panel min-w-0 flex-1 rounded-xl px-4 py-3">
        {resp.note && (
          <div className="mb-2 flex items-start gap-2 rounded-md border border-border bg-raised px-2.5 py-1.5 text-[12px] text-muted-foreground">
            <Info className="mt-px size-3.5 shrink-0" /> {resp.note}
          </div>
        )}
        <div className="nx-prose text-[13.5px] leading-relaxed">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{resp.answer}</ReactMarkdown>
        </div>
        <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1.5 border-t border-border pt-2 text-[12px] font-medium text-muted-foreground">
          <span>{resp.mode === "llm" ? resp.model : "rule-based"}</span>
          {resp.sim_time && <span title="Simulated time the data refers to">sim {resp.sim_time}</span>}
          <span>{(resp.elapsed_ms / 1000).toFixed(1)} s</span>
          {resp.tool_calls.length > 0 && (
            <button type="button" onClick={() => setTrace((t) => !t)} className="flex items-center gap-1 text-brand/80 hover:text-brand" aria-expanded={trace}>
              <Wrench className="size-3" /> {resp.tool_calls.length} tool call{resp.tool_calls.length > 1 ? "s" : ""}
              <ChevronRight className={cn("size-3 transition", trace && "rotate-90")} />
            </button>
          )}
          {resp.grounding.ungrounded.length === 0 && (
            <span className="ml-auto">
              <Grounding g={resp.grounding} mode={resp.mode} />
            </span>
          )}
        </div>
        {resp.grounding.ungrounded.length > 0 && (
          <div className="mt-2">
            <Grounding g={resp.grounding} mode={resp.mode} />
          </div>
        )}
        {trace && (
          <ul className="mt-2 space-y-1">
            {resp.tool_calls.map((t, i) => <ToolCallRow key={i} t={t} />)}
          </ul>
        )}
      </div>
    </div>
  );
}

function Pending({ question }: { question: string }) {
  // Measured from a timestamp: background tabs throttle timers, so counting ticks drifts.
  const [secs, setSecs] = useState(0);
  useEffect(() => {
    const start = Date.now();
    const t = setInterval(() => setSecs(Math.floor((Date.now() - start) / 1000)), 500);
    return () => clearInterval(t);
  }, []);
  const whatIf = WHAT_IF.test(question);
  return (
    <div className="flex gap-3">
      <div className="mt-0.5 grid size-7 shrink-0 place-items-center rounded-lg border border-border bg-raised">
        <Loader2 className="size-4 animate-spin text-brand" />
      </div>
      <div className="panel rounded-xl px-4 py-3 text-xs text-muted-foreground">
        {whatIf && secs > 4 ? "Running a what-if simulation (baseline and scenario, two simulated hours each)…" : "Reading the live city data…"}
        <span className="ml-2 font-mono num">{secs}s</span>
        {whatIf && secs > 4 && <div className="mt-1 text-[12px]">This usually takes 20–60 seconds.</div>}
      </div>
    </div>
  );
}

// ------------------------------------------------------------------ page

export function IntelligenceChat() {
  const [status, setStatus] = useState<AssistantStatus | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [input, setInput] = useState("");
  const [pending, setPending] = useState<string | null>(null);
  const bottom = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const loadStatus = useCallback(async () => {
    try {
      setStatus(await api.assistantStatus());
      setStatusError(null);
    } catch (e) {
      setStatusError((e as Error).message);
    }
  }, []);

  useEffect(() => {
    // Initial fetch, then a slow refresh so the badge reflects config changes.
    const first = setTimeout(loadStatus, 0);
    const t = setInterval(loadStatus, 30_000);
    return () => {
      clearTimeout(first);
      clearInterval(t);
    };
  }, [loadStatus]);

  useEffect(() => {
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    bottom.current?.scrollIntoView({ behavior: reduce ? "auto" : "smooth", block: "end" });
  }, [messages, pending]);

  const send = async (text: string) => {
    const q = text.trim();
    if (!q || pending) return;
    // Conversation so far as plain turns, newest last, within the service's limits.
    const history: ChatTurn[] = messages
      .flatMap((m): ChatTurn[] =>
        m.role === "user" ? [{ role: "user", content: m.text }]
        : m.role === "assistant" ? [{ role: "assistant", content: m.resp.answer.slice(0, 8000) }]
        : [],
      )
      .slice(-MAX_HISTORY);
    setMessages((ms) => [...ms, { id: nextId++, role: "user", text: q }]);
    setInput("");
    setPending(q);
    try {
      const resp = await api.chat(q, history);
      setMessages((ms) => [...ms, { id: nextId++, role: "assistant", resp }]);
      setStatusError(null);
    } catch (e) {
      setMessages((ms) => [...ms, { id: nextId++, role: "error", text: (e as Error).message }]);
    } finally {
      setPending(null);
      inputRef.current?.focus();
    }
  };

  const empty = messages.length === 0 && !pending;

  return (
    <div className="mx-auto flex h-full max-w-[1400px] gap-5 p-5">
      <section className="flex min-w-0 flex-1 flex-col">
        <header className="mb-4 flex flex-wrap items-center gap-3">
          <div>
            <div className="eyebrow">Ask the city</div>
            <h1 className="mt-1 text-[22px] font-semibold tracking-[-0.01em]">City Intelligence</h1>
          </div>
          <div className="ml-auto flex items-center gap-2">
            <ModeBadge status={status} error={statusError} />
            {messages.length > 0 && (
              <button type="button" onClick={() => setMessages([])} disabled={!!pending}
                className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-muted-foreground transition hover:bg-accent hover:text-foreground disabled:opacity-40">
                <RotateCcw className="size-3.5" /> New conversation
              </button>
            )}
          </div>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto pr-1" aria-live="polite">
          {empty ? (
            <motion.div initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }} className="mx-auto max-w-2xl pt-10 text-center">
              <div className="mx-auto mb-4 grid size-12 place-items-center rounded-2xl border border-border bg-raised">
                <MessageSquareText className="size-6 text-brand" />
              </div>
              <h2 className="text-base font-medium">Ask about traffic, energy, air quality or a what-if</h2>
              <p className="mx-auto mt-1.5 max-w-lg text-xs leading-relaxed text-muted-foreground">
                Answers come from the live simulation, the forecasting models and the scenario engine — never from
                guesses. Expand the tool trace under an answer to see the exact data it used.
              </p>
              <div className="mt-6 flex flex-wrap justify-center gap-2">
                {SUGGESTED.map((s) => (
                  <button key={s} type="button" onClick={() => send(s)} disabled={!!statusError}
                    className="rounded-md border border-border bg-raised px-3 py-1.5 text-xs text-foreground/85 transition hover:border-brand/40 hover:bg-brand/10 hover:text-foreground disabled:opacity-40">
                    {s}
                  </button>
                ))}
              </div>
              {statusError && (
                <p className="mt-6 text-xs text-crit">
                  The assistant is unreachable ({statusError}). Start it with <code className="font-mono">nexus-assistant serve</code> and
                  set <code className="font-mono">ASSISTANT_URL</code> on the gateway.
                </p>
              )}
            </motion.div>
          ) : (
            <div className="mx-auto max-w-3xl space-y-4 pb-2">
              {messages.map((m) =>
                m.role === "user" ? (
                  <div key={m.id} className="flex justify-end">
                    <div className="max-w-[80%] whitespace-pre-wrap rounded-xl rounded-br-sm border border-border bg-raised px-3.5 py-2 text-[13.5px]">
                      {m.text}
                    </div>
                  </div>
                ) : m.role === "assistant" ? (
                  <AssistantMessage key={m.id} resp={m.resp} />
                ) : (
                  <div key={m.id} className="flex items-start gap-2 rounded-lg border border-crit/40 bg-crit/10 px-3 py-2 text-xs text-crit">
                    <XCircle className="mt-px size-3.5 shrink-0" /> Couldn&apos;t get an answer: {m.text}
                  </div>
                ),
              )}
              {pending && <Pending question={pending} />}
              <div ref={bottom} />
            </div>
          )}
        </div>

        <form
          className="mx-auto mt-3 w-full max-w-3xl"
          onSubmit={(e) => {
            e.preventDefault();
            send(input);
          }}
        >
          <div className="panel flex items-end gap-2 rounded-xl p-2 focus-within:border-brand/40">
            <label htmlFor="ask" className="sr-only">Ask City Intelligence</label>
            <textarea
              id="ask"
              ref={inputRef}
              value={input}
              onChange={(e) => setInput(e.target.value.slice(0, MAX_MESSAGE))}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault();
                  send(input);
                }
              }}
              rows={1}
              placeholder="Ask about the city… (Enter to send, Shift+Enter for a new line)"
              className="max-h-40 min-h-9 flex-1 resize-none bg-transparent px-2 py-2 text-sm outline-none placeholder:text-muted-foreground/60 field-sizing-content"
            />
            <button type="submit" disabled={!input.trim() || !!pending} aria-label="Send"
              className="grid size-9 shrink-0 place-items-center rounded-lg bg-primary text-primary-foreground transition hover:bg-primary/85 disabled:opacity-30">
              {pending ? <Loader2 className="size-4 animate-spin" /> : <ArrowUp className="size-4" />}
            </button>
          </div>
          <div className="mt-1.5 flex justify-between px-1 text-[11.5px] text-muted-foreground/70">
            <span>Synthetic city · answers describe the simulation, not a real place</span>
            {input.length > MAX_MESSAGE * 0.8 && <span className="font-mono num">{input.length}/{MAX_MESSAGE}</span>}
          </div>
        </form>
      </section>

      <aside className="hidden w-[280px] shrink-0 flex-col gap-3 xl:flex">
        <div className="panel rounded-xl p-4 text-xs leading-relaxed">
          <div className="label-caps mb-2">How answers are made</div>
          <ol className="list-decimal space-y-1.5 pl-4 text-muted-foreground">
            <li>The question is matched to tools that read the live gateway: telemetry, forecasts, anomalies, model metrics and the scenario engine.</li>
            <li>{status?.mode === "llm" ? `${status.model} reads the tool results and writes the answer.` : "A deterministic template writes the answer from the tool results."}</li>
            <li>Every number in the answer is checked against the tool data; any that don&apos;t match are flagged.</li>
          </ol>
        </div>
        <div className="panel rounded-xl p-4 text-xs leading-relaxed">
          <div className="label-caps mb-2 text-warn/80">Limits</div>
          <ul className="list-disc space-y-1.5 pl-4 text-muted-foreground">
            <li>All data is synthetic. Forecasts come from models trained on other simulated cities.</li>
            <li>The simulation does not record why congestion happens, so explanations stay with what the data shows.</li>
            <li>What-if answers run a real scenario; they take up to a minute and also appear in the Scenario Lab.</li>
            <li>No conversation is stored. Only the last {MAX_HISTORY} turns are sent with each question.</li>
          </ul>
        </div>
        {status && (
          <div className="panel rounded-xl p-4 font-mono text-[11.5px] leading-relaxed text-muted-foreground">
            <div className="label-caps mb-2">Service</div>
            <div>mode: {status.mode}</div>
            {status.model && <div>model: {status.model}</div>}
            {status.fallback_model && <div>fallback: {status.fallback_model}</div>}
            <div>gateway: {status.gateway_ok ? "ok" : "unreachable"}</div>
            {status.sim_time && <div>sim time: {status.sim_time}</div>}
          </div>
        )}
      </aside>
    </div>
  );
}
