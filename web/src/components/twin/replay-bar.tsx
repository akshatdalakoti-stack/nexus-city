"use client";

import { useEffect, useRef, useState } from "react";
import { History, Radio } from "lucide-react";
import { Slider } from "@/components/ui/slider";
import { api } from "@/lib/api";
import { hhmm, simClock } from "@/lib/format";
import { useLive } from "@/lib/store";
import type { Telemetry, TelemetryRange } from "@/lib/types";
import { cn } from "@/lib/utils";

interface Props {
  replay: { at: string; telemetry: Telemetry | null } | null;
  onChange: (r: { at: string; telemetry: Telemetry | null } | null) => void;
}

/**
 * Switches the map between live telemetry and replay of recorded telemetry.
 * Replay reads snapshots the gateway already holds; it never re-runs or
 * alters the simulation.
 */
export function ReplayBar({ replay, onChange }: Props) {
  const [range, setRange] = useState<TelemetryRange | null>(null);
  const [error, setError] = useState<string | null>(null);
  const seq = useLive((s) => s.telemetrySeq);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Keep the replayable window current while in replay mode.
  useEffect(() => {
    if (!replay) return;
    api.range().then(setRange, (e: Error) => setError(e.message));
  }, [replay, seq]);

  const enter = async () => {
    setError(null);
    try {
      const r = await api.range();
      setRange(r);
      if (!r.to) {
        setError("No telemetry recorded yet");
        return;
      }
      const env = await api.telemetryAt(r.to);
      onChange({ at: r.to, telemetry: env.payload });
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const seek = (ms: number) => {
    const at = new Date(ms).toISOString().replace(".000Z", "Z");
    onChange({ at, telemetry: replay?.telemetry ?? null });
    clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      api.telemetryAt(at).then(
        (env) => onChange({ at, telemetry: env.payload }),
        (e: Error) => setError(e.message),
      );
    }, 120);
  };

  const from = range?.from ? new Date(range.from).getTime() : 0;
  const to = range?.to ? new Date(range.to).getTime() : 0;
  const at = replay ? new Date(replay.at).getTime() : to;
  const c = simClock(replay?.at);

  return (
    <div className="panel absolute bottom-4 left-4 right-16 flex h-16 items-center gap-4 rounded-xl px-4">
      <div className="flex rounded-lg border border-border bg-raised p-0.5" role="group" aria-label="Map mode">
        <button
          type="button"
          onClick={() => onChange(null)}
          aria-pressed={!replay}
          className={cn("flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs", !replay ? "bg-[#2a2a2e] text-foreground" : "text-muted-foreground hover:text-foreground")}
        >
          <Radio className="size-3.5" /> Live
        </button>
        <button
          type="button"
          onClick={enter}
          aria-pressed={!!replay}
          className={cn("flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs", replay ? "bg-warn/15 text-warn" : "text-muted-foreground hover:text-foreground")}
        >
          <History className="size-3.5" /> Replay
        </button>
      </div>
      {replay && range?.from && range.to ? (
        <>
          <span className="w-12 font-mono text-[12px] text-muted-foreground">{hhmm(range.from)}</span>
          <Slider
            className="flex-1"
            min={from}
            max={Math.max(to, from + 60_000)}
            step={60_000}
            value={[Math.min(Math.max(at, from), to)]}
            onValueChange={(v: number | readonly number[]) => seek(Array.isArray(v) ? v[0] : (v as number))}
            aria-label="Replay time"
          />
          <span className="w-12 font-mono text-[12px] text-muted-foreground">{hhmm(range.to)}</span>
          <span className="w-36 text-right font-mono text-sm text-warn num">
            {c.day} {c.time}
          </span>
        </>
      ) : (
        <p className="flex-1 text-xs text-muted-foreground">
          {error ? (
            <span className="text-crit">{error}</span>
          ) : (
            "Live view: map values update each simulated minute; vehicles stream continuously. Switch to Replay to scrub through recorded telemetry for this run."
          )}
        </p>
      )}
    </div>
  );
}
