"use client";

import { useState } from "react";
import { Pause, Play, RotateCcw, Thermometer, Wind } from "lucide-react";
import { api } from "@/lib/api";
import { simClock } from "@/lib/format";
import { useLive } from "@/lib/store";
import { cn } from "@/lib/utils";

const SPEEDS = [1, 2, 5, 10, 60];

function ConnectionStatus() {
  const conn = useLive((s) => s.conn);
  const map = {
    open: { label: "Live", dot: "bg-ok", text: "text-foreground" },
    connecting: { label: "Connecting", dot: "bg-warn animate-pulse", text: "text-warn" },
    closed: { label: "Gateway offline", dot: "bg-crit", text: "text-crit" },
  }[conn];
  return (
    <div className={cn("flex items-center gap-2 rounded-md border border-border px-2 py-1 text-[12.5px] font-medium", map.text)} role="status">
      <span className={cn("size-2 rounded-full", map.dot)} aria-hidden />
      {map.label}
    </div>
  );
}

export function SimClockBar({ title }: { title: string }) {
  const simTime = useLive((s) => s.simTime);
  const clock = useLive((s) => s.clock);
  const weather = useLive((s) => s.telemetry?.weather);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const c = simClock(simTime);

  const send = async (fn: () => Promise<unknown>) => {
    setPending(true);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setPending(false);
    }
  };

  return (
    // Wraps onto two lines on narrow screens instead of overflowing.
    <header className="flex min-h-[52px] shrink-0 flex-wrap items-center gap-x-4 gap-y-2 border-b border-border bg-background px-3 py-2 sm:px-5 md:flex-nowrap md:py-0">
      <div className="hidden items-center gap-2 whitespace-nowrap text-[13.5px] md:flex">
        <span className="hidden text-muted-foreground xl:inline">Nexus City</span>
        <span className="hidden text-muted-foreground/60 xl:inline">/</span>
        <span className="font-medium text-foreground">{title}</span>
      </div>

      <div className="flex items-center gap-2 md:ml-4">
        <div className="flex items-baseline gap-2 rounded-md border border-border bg-surface px-2.5 py-1" title="Simulated time">
          <span className="text-[12.5px] text-muted-foreground">{c.day}</span>
          <span className="font-mono text-[14px] font-medium text-foreground num">
            {c.time}
            <span className="text-muted-foreground">:{c.seconds}</span>
          </span>
          <span className="hidden text-[12px] text-muted-foreground xl:inline">{c.date}</span>
        </div>
        {clock?.paused && <span className="rounded-md bg-warn/15 px-1.5 py-0.5 text-[12px] font-medium text-warn">Paused</span>}
      </div>

      <div className="flex items-center rounded-lg border border-border bg-surface p-0.5" role="group" aria-label="Simulation speed">
        <button
          type="button"
          disabled={!clock || pending}
          onClick={() => send(() => api.control({ action: clock?.paused ? "resume" : "pause" }))}
          className="grid size-7 place-items-center rounded-md text-foreground transition hover:bg-accent disabled:opacity-40"
          aria-label={clock?.paused ? "Resume simulation" : "Pause simulation"}
          title={clock?.paused ? "Resume" : "Pause"}
        >
          {clock?.paused ? <Play className="size-3.5" /> : <Pause className="size-3.5" />}
        </button>
        <span className="mx-0.5 h-4 w-px bg-border" aria-hidden />
        {SPEEDS.map((s) => {
          const selected = clock?.speed === s;
          return (
            <button
              key={s}
              type="button"
              disabled={!clock || pending}
              onClick={() => send(() => api.control({ action: "speed", speed: s }))}
              aria-pressed={selected}
              className={cn(
                "h-7 min-w-9 rounded-md px-2 text-[12.5px] font-medium tabular-nums transition disabled:opacity-40",
                selected
                  ? clock?.paused
                    ? "bg-raised text-muted-foreground"
                    : "bg-[#2a2a2e] text-foreground shadow-[0_1px_0_rgb(255_255_255/0.06)_inset]"
                  : "text-muted-foreground hover:text-foreground",
              )}
              title={s === 60 ? "60× demo speed" : `${s}× speed`}
            >
              {s}×
            </button>
          );
        })}
      </div>
      {error && <span className="truncate text-[12.5px] text-crit">{error}</span>}

      <div className="ml-auto flex items-center gap-3">
        {weather && (
          <div className="hidden items-center gap-3 text-[12.5px] text-muted-foreground lg:flex">
            <span className="flex items-center gap-1.5 whitespace-nowrap num">
              <Thermometer className="size-3.5" aria-hidden />
              {weather.temp_c.toFixed(1)}°C
            </span>
            <span className="flex items-center gap-1.5 whitespace-nowrap num">
              <Wind className="size-3.5" style={{ transform: `rotate(${weather.wind_dir_deg + 180 - 45}deg)` }} aria-hidden />
              {weather.wind_speed_ms.toFixed(1)} m/s
            </span>
          </div>
        )}
        <button
          type="button"
          disabled={!clock || pending}
          onClick={() => {
            if (window.confirm("Restart the simulation from 05:30 with the same seed? Recorded history for this run is cleared.")) {
              send(() => api.reset());
            }
          }}
          className="flex h-7 items-center gap-1.5 rounded-md border border-border px-2 text-[12.5px] text-muted-foreground transition hover:bg-accent hover:text-foreground disabled:opacity-40"
          title={clock ? `Restart run (seed ${clock.seed})` : "Restart run"}
        >
          <RotateCcw className="size-3.5" aria-hidden /> Restart
        </button>
        <ConnectionStatus />
      </div>
    </header>
  );
}
