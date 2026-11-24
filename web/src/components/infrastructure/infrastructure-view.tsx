"use client";

import { useCallback, useEffect, useState } from "react";
import {
  Activity,
  AlertTriangle,
  Boxes,
  CheckCircle2,
  Cpu,
  ExternalLink,
  Gauge,
  Layers,
  Loader2,
  MemoryStick,
  Rocket,
  Server,
  XCircle,
} from "lucide-react";
import { api } from "@/lib/api";
import type { InfraAutoscaler, InfraInfo, InfraPod, InfraSeriesResponse, RuntimeInfo } from "@/lib/types";
import { Card as NxCard } from "@/components/ui/nx";
import { cn } from "@/lib/utils";
import { MetricChart, PALETTE, splitBy } from "./metric-chart";

// Heatwave temperature deltas, one scenario each. A six-hour scenario runs in
// ~5 s, so a burst must be big enough to outlast the autoscaler's reaction
// time (~30 s): with 6, a single worker drained the queue before KEDA added a
// second; 12 reached 3 workers in a measured run.
const BURST = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12];

function age(iso?: string) {
  if (!iso) return "—";
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 90) return `${Math.round(s)}s`;
  if (s < 5400) return `${Math.round(s / 60)}m`;
  if (s < 172800) return `${(s / 3600).toFixed(1)}h`;
  return `${Math.round(s / 86400)}d`;
}

function Panel({ title, icon, children, right, className }: {
  title: string;
  icon: typeof Server;
  children: React.ReactNode;
  right?: React.ReactNode;
  className?: string;
}) {
  return (
    <NxCard title={title} icon={icon} action={right} className={className}>
      {children}
    </NxCard>
  );
}

function Tile({ label, value, sub, tone }: { label: string; value: string; sub?: string; tone?: "ok" | "warn" | "crit" }) {
  return (
    <div className="rounded-lg border border-border bg-raised px-3 py-2.5">
      <div className="label-caps">{label}</div>
      <div className={cn("mt-1 font-mono text-xl num", tone === "ok" && "text-ok", tone === "warn" && "text-warn", tone === "crit" && "text-crit")}>
        {value}
      </div>
      {sub && <div className="text-[12px] text-muted-foreground">{sub}</div>}
    </div>
  );
}

/** used / limit bar; the tick marks the request. */
function UsageBar({ used, request, limit, unit }: { used?: number; request: number; limit: number; unit: string }) {
  if (used === undefined) return <span className="text-[12px] text-muted-foreground">not measured</span>;
  const max = limit || request || used || 1;
  const pct = Math.min(1, used / max);
  const fmt = (v: number) => (v >= 100 ? Math.round(v).toString() : v.toFixed(v >= 10 ? 0 : 1));
  return (
    <div className="min-w-28">
      <div className="flex items-baseline justify-between gap-2 font-mono text-[12px] num">
        <span>{fmt(used)}{unit}</span>
        <span className="text-muted-foreground">{limit ? `/ ${fmt(limit)}${unit}` : "no limit"}</span>
      </div>
      <div className="relative mt-1 h-1.5 overflow-hidden rounded-full bg-secondary">
        <div className={cn("h-full rounded-full", pct > 0.85 ? "bg-crit" : pct > 0.6 ? "bg-warn" : "bg-brand")} style={{ width: `${pct * 100}%` }} />
        {request > 0 && limit > 0 && <div className="absolute inset-y-0 w-px bg-foreground/50" style={{ left: `${Math.min(1, request / limit) * 100}%` }} />}
      </div>
    </div>
  );
}

function StatusBadge({ pod }: { pod: InfraPod }) {
  const good = pod.ready && pod.status === "Running";
  const bad = /Crash|Error|BackOff|Failed|OOM|Err/i.test(pod.status);
  return (
    <span className={cn("inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-mono text-[11.5px]",
      good ? "bg-ok/10 text-ok" : bad ? "bg-crit/10 text-crit" : "bg-warn/10 text-warn")}>
      {good ? <CheckCircle2 className="size-3" /> : bad ? <XCircle className="size-3" /> : <Loader2 className="size-3 animate-spin" />}
      {pod.ready || pod.status !== "Running" ? pod.status : "Running · not ready"}
    </span>
  );
}

function AutoscalerCard({ a, queue }: { a: InfraAutoscaler; queue?: RuntimeInfo["scenario_queue"] }) {
  const slots = Array.from({ length: a.max }, (_, i) => i < (a.current ?? 0));
  const md = a.trigger_metadata ?? {};
  return (
    <div className="space-y-3">
      <div className="flex items-end gap-4">
        <div>
          <div className="label-caps">{a.target} replicas</div>
          <div className="font-mono text-4xl num">{a.current ?? "—"}</div>
        </div>
        <div className="flex gap-1.5 pb-2" aria-label={`${a.current ?? 0} of ${a.max} replicas`}>
          {slots.map((on, i) => (
            <div key={i} className={cn("size-6 rounded-md border", on ? "border-brand/60 bg-brand/25" : "border-border bg-raised")} />
          ))}
        </div>
        <div className="ml-auto text-right font-mono text-[12px] text-muted-foreground">
          <div>min {a.min} · max {a.max}</div>
          <div className={a.active ? "text-ok" : ""}>{a.active === undefined ? "state unknown" : a.active ? "active: work queued" : "idle: scaled to minimum"}</div>
        </div>
      </div>
      <div className="grid grid-cols-3 gap-2">
        <Tile label="Waiting" value={queue ? String(queue.waiting) : "—"} sub="not picked up" tone={queue && queue.waiting > 0 ? "warn" : undefined} />
        <Tile label="Running" value={queue ? String(queue.running) : "—"} sub="being simulated" />
        <Tile label="Desired" value={a.desired !== undefined ? String(a.desired) : "—"} sub="HPA target" />
      </div>
      <p className="text-[12px] leading-relaxed text-muted-foreground">
        KEDA <span className="font-mono">{a.trigger}</span> scaler on <span className="font-mono">{md.stream ?? "?"}</span>
        {md.consumerGroup && <> (group <span className="font-mono">{md.consumerGroup}</span>)</>}: one worker per waiting scenario, kept while scenarios are in flight,
        scaled to {a.min} when the queue is empty.
        {a.last_scale_time && <> Last scaled {age(a.last_scale_time)} ago.</>}
      </p>
    </div>
  );
}

function OutsideKubernetes({ runtime }: { runtime: RuntimeInfo | null }) {
  const rows: [string, string, boolean | undefined][] = runtime
    ? [
        ["Gateway", `${runtime.gateway.hostname} · up ${Math.round(runtime.gateway.uptime_s / 60)} min · ${runtime.gateway.heap_mb.toFixed(0)} MB heap`, true],
        ["Redis", runtime.redis.ok ? `${runtime.redis.latency_ms.toFixed(1)} ms · ${runtime.redis.used_memory_mb.toFixed(0)} MB` : runtime.redis.error ?? "down", runtime.redis.ok],
        ["Simulation", runtime.simulation.alive ? `heartbeat ${runtime.simulation.heartbeat_age_s?.toFixed(1)} s ago` : "no heartbeat", runtime.simulation.alive],
        ["PostgreSQL", runtime.database?.configured ? (runtime.database.ok ? `${runtime.database.latency_ms?.toFixed(1)} ms` : runtime.database.error ?? "down") : "not configured", runtime.database?.ok],
        ["Scenario queue", runtime.scenario_queue ? `${runtime.scenario_queue.waiting} waiting · ${runtime.scenario_queue.running} running · ${runtime.scenario_queue.consumers} workers registered` : "unavailable", !!runtime.scenario_queue],
      ]
    : [];
  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <Panel title="Not running on Kubernetes" icon={Boxes}>
        <p className="text-sm leading-relaxed text-muted-foreground">
          The gateway is running outside a Kubernetes cluster (Docker Compose or local development), so there are no pods,
          autoscalers or cluster metrics to show. Deploy to Docker Desktop&apos;s Kubernetes to see them:
        </p>
        <pre className="mt-3 overflow-x-auto rounded-lg border border-border bg-raised p-3 font-mono text-[12px] leading-relaxed">
{`powershell -File deploy\\k8s\\build-images.ps1
powershell -File deploy\\k8s\\deploy.ps1`}
        </pre>
      </Panel>
      <Panel title="Runtime (from the gateway)" icon={Activity}>
        {runtime ? (
          <ul className="space-y-2 text-xs">
            {rows.map(([k, v, ok]) => (
              <li key={k} className="flex items-center gap-2">
                {ok ? <CheckCircle2 className="size-3.5 text-ok" /> : ok === false ? <XCircle className="size-3.5 text-crit" /> : <span className="size-3.5" />}
                <span className="w-28 text-muted-foreground">{k}</span>
                <span className="font-mono">{v}</span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-xs text-muted-foreground">Loading…</p>
        )}
      </Panel>
    </div>
  );
}

// ------------------------------------------------------------------ page

export function InfrastructureView() {
  const [info, setInfo] = useState<InfraInfo | null>(null);
  const [runtime, setRuntime] = useState<RuntimeInfo | null>(null);
  const [series, setSeries] = useState<InfraSeriesResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [burst, setBurst] = useState<{ busy: boolean; msg?: string }>({ busy: false });

  const load = useCallback(async () => {
    try {
      const [i, r] = await Promise.all([api.infra(), api.runtime().catch(() => null)]);
      setInfo(i);
      setRuntime(r);
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }, []);
  const loadSeries = useCallback(async () => {
    try {
      setSeries(await api.infraSeries(30));
    } catch {
      setSeries(null);
    }
  }, []);

  useEffect(() => {
    const first = setTimeout(() => {
      load();
      loadSeries();
    }, 0);
    const a = setInterval(load, 4000);
    const b = setInterval(loadSeries, 15000);
    return () => {
      clearTimeout(first);
      clearInterval(a);
      clearInterval(b);
    };
  }, [load, loadSeries]);

  const runBurst = async () => {
    setBurst({ busy: true });
    try {
      for (const d of BURST) {
        await api.createScenario({ name: `Load test: heatwave +${d} °C`, kind: "heatwave", params: { temp_delta_c: d }, duration_min: 360 });
      }
      setBurst({ busy: false, msg: `Queued ${BURST.length} six-hour heatwave scenarios. Workers scale up within ~30 s and back to zero a minute after the queue empties; results appear in the Scenario Lab.` });
      load();
    } catch (e) {
      setBurst({ busy: false, msg: `Could not queue: ${(e as Error).message}` });
    }
  };

  const k8s = info?.mode === "kubernetes";
  const pods = info?.pods ?? [];
  const readyPods = pods.filter((p) => p.ready).length;
  const restarts = pods.reduce((s, p) => s + p.restarts, 0);
  const measured = pods.filter((p) => p.cpu_millicores !== undefined);
  const cpu = measured.reduce((s, p) => s + (p.cpu_millicores ?? 0), 0);
  const mem = pods.reduce((s, p) => s + (p.memory_mib ?? 0), 0);
  const memLimit = pods.reduce((s, p) => s + p.memory_limit_mib, 0);
  const cpuReq = pods.reduce((s, p) => s + p.cpu_request_millicores, 0);
  const s = series?.series ?? {};

  return (
    <div className="mx-auto max-w-[1400px] space-y-5 px-4 py-6 sm:px-6">
      <header className="flex flex-wrap items-center gap-3">
        <div>
          <div className="eyebrow">Platform</div>
          <h1 className="mt-1 text-[22px] font-semibold tracking-[-0.01em]">Infrastructure</h1>
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          {info && (
            <span className={cn("rounded-md border px-2 py-1 text-[12px] font-medium", k8s ? "border-border bg-raised text-foreground" : "border-border text-muted-foreground")}>
              {k8s ? `Kubernetes · namespace ${info.namespace}` : "outside Kubernetes"}
            </span>
          )}
          {info?.prometheus.configured && (
            <span className={cn("rounded-md border px-2 py-1 text-[12px] font-medium", info.prometheus.ok ? "border-ok/30 text-ok" : "border-crit/40 text-crit")}>
              Prometheus {info.prometheus.ok ? "ok" : "unreachable"}
            </span>
          )}
          {info?.grafana_url && k8s && (
            <a href={info.grafana_url} target="_blank" rel="noreferrer"
              className="flex items-center gap-1.5 rounded-md border border-border px-2 py-1 text-xs text-foreground/85 transition hover:border-brand/40 hover:text-foreground">
              Grafana <ExternalLink className="size-3" />
            </a>
          )}
          {info && <span className="text-[12px] font-medium text-muted-foreground">checked {new Date(info.checked_at).toLocaleTimeString()}</span>}
        </div>
      </header>

      {error && (
        <div className="flex items-center gap-2 rounded-lg border border-crit/40 bg-crit/10 px-3 py-2 text-xs text-crit">
          <XCircle className="size-3.5" /> {error}
        </div>
      )}
      {info?.error && (
        <div className="flex items-center gap-2 rounded-lg border border-warn/40 bg-warn/10 px-3 py-2 text-xs text-warn">
          <AlertTriangle className="size-3.5" /> {info.error}
        </div>
      )}

      {!info && !error && <p className="text-xs text-muted-foreground">Loading…</p>}
      {info && !k8s && <OutsideKubernetes runtime={runtime} />}

      {info && k8s && (
        <>
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            <Tile label="Pods ready" value={`${readyPods}/${pods.length}`} tone={readyPods === pods.length ? "ok" : "warn"} sub={`${info.workloads.length} workloads`} />
            <Tile label="Restarts" value={String(restarts)} tone={restarts > 0 ? "warn" : undefined} sub="all containers, since created" />
            <Tile label="CPU in use" value={measured.length ? `${(cpu / 1000).toFixed(2)} cores` : "—"} sub={`requests ${(cpuReq / 1000).toFixed(2)} cores`} />
            <Tile label="Memory in use" value={measured.length ? `${(mem / 1024).toFixed(2)} GiB` : "—"} sub={`limits ${(memLimit / 1024).toFixed(1)} GiB`} />
          </div>

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)]">
            <Panel title="Autoscaling (KEDA)" icon={Gauge}
              right={
                <button type="button" onClick={runBurst} disabled={burst.busy}
                  className="flex items-center gap-1.5 rounded-md border border-brand/40 bg-brand/10 px-2 py-1 text-xs text-brand transition hover:bg-brand/20 disabled:opacity-40"
                  title="Queues twelve real six-hour heatwave scenarios">
                  {burst.busy ? <Loader2 className="size-3.5 animate-spin" /> : <Rocket className="size-3.5" />} Queue a burst
                </button>
              }>
              {info.autoscalers.length === 0 ? (
                <p className="text-xs text-muted-foreground">No KEDA ScaledObject found in this namespace.</p>
              ) : (
                info.autoscalers.map((a) => <AutoscalerCard key={a.name} a={a} queue={runtime?.scenario_queue} />)
              )}
              {burst.msg && <p className="mt-3 text-[12px] text-brand/90">{burst.msg}</p>}
            </Panel>
            <Panel title="Queue vs workers · last 30 min" icon={Activity}>
              {series ? (
                <MetricChart step format={(v) => v.toFixed(0)} height={230} lines={[
                  { key: "waiting", label: "waiting", color: PALETTE[3], series: s.queue_waiting },
                  { key: "running", label: "running", color: PALETTE[2], series: s.queue_running },
                  { key: "workers", label: "worker pods", color: PALETTE[0], series: s.scenario_workers },
                ]} />
              ) : (
                <p className="text-xs text-muted-foreground">Prometheus series unavailable.</p>
              )}
            </Panel>
          </div>

          <Panel title="Pods" icon={Server}
            right={<span className="text-[12px] font-medium text-muted-foreground">usage from {info.usage_source === "none" ? "nowhere (not measured)" : info.usage_source}</span>}>
            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-left">
                    {["Component", "Pod", "Status", "Restarts", "CPU (millicores)", "Memory (MiB)", "Age"].map((h) => (
                      <th key={h} className="label-caps pb-2 pr-4 font-normal">{h}</th>
                    ))}
                  </tr>
                </thead>
                <tbody className="[&_td]:py-1.5 [&_td]:pr-4 [&_tr]:border-t [&_tr]:border-border">
                  {pods.map((p) => (
                    <tr key={p.name}>
                      <td className="font-medium">{p.component}</td>
                      <td className="max-w-56 truncate font-mono text-[12px] text-muted-foreground" title={p.name}>{p.name}</td>
                      <td><StatusBadge pod={p} /></td>
                      <td className={cn("font-mono num", p.restarts > 0 && "text-warn")}>{p.restarts}</td>
                      <td><UsageBar used={p.cpu_millicores} request={p.cpu_request_millicores} limit={p.cpu_limit_millicores} unit="m" /></td>
                      <td><UsageBar used={p.memory_mib} request={p.memory_request_mib} limit={p.memory_limit_mib} unit="" /></td>
                      <td className="font-mono text-muted-foreground">{age(p.started_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {info.usage_source === "prometheus" && (
              <p className="mt-2 text-[12px] text-muted-foreground">
                CPU and memory are each service&apos;s own process metrics. Redis, PostgreSQL, Grafana and the web server don&apos;t export them, so they show as not measured.
              </p>
            )}
          </Panel>

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <Panel title="CPU by component" icon={Cpu}>
              <MetricChart stacked format={(v) => `${v.toFixed(0)}m`} lines={splitBy(s.cpu_by_component, "component")} />
            </Panel>
            <Panel title="Memory by component" icon={MemoryStick}>
              <MetricChart stacked format={(v) => `${v.toFixed(0)} MiB`} lines={splitBy(s.memory_by_component, "component")} />
            </Panel>
            <Panel title="Gateway requests per second" icon={Activity}>
              <MetricChart format={(v) => v.toFixed(1)} lines={[{ key: "rps", label: "requests/s", color: PALETTE[1], series: s.gateway_rps }]} />
            </Panel>
            <Panel title="Simulation steps per second" icon={Activity}>
              <MetricChart format={(v) => v.toFixed(0)} lines={[{ key: "steps", label: "steps/s", color: PALETTE[4], series: s.sim_steps_per_s }]} />
            </Panel>
          </div>

          <Panel title="Workloads" icon={Layers}>
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4">
              {info.workloads.map((w) => (
                <div key={w.name} className="rounded-lg border border-border bg-raised px-3 py-2">
                  <div className="flex items-center gap-2 text-xs">
                    <span className="font-medium">{w.name}</span>
                    <span className="ml-auto font-mono text-[12px] text-muted-foreground">{w.kind}</span>
                  </div>
                  <div className={cn("mt-1 font-mono text-sm num", w.ready === w.desired ? "text-ok" : "text-warn")}>
                    {w.ready}/{w.desired} ready
                  </div>
                  <div className="truncate font-mono text-[12px] text-muted-foreground" title={w.image}>{w.image}</div>
                </div>
              ))}
            </div>
          </Panel>

          {info.missing && info.missing.length > 0 && (
            <p className="text-[12px] text-muted-foreground">Not shown: {info.missing.join("; ")}.</p>
          )}
          {series?.errors && series.errors.length > 0 && (
            <p className="text-[12px] text-warn">Some charts failed: {series.errors.join("; ")}</p>
          )}
        </>
      )}
    </div>
  );
}
