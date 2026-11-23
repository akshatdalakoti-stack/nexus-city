"use client";

import { useEffect, useState } from "react";
import { AlertTriangle, CheckCircle2, Database, FlaskConical, Microscope } from "lucide-react";
import { Bar, BarChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { cursorFill, tooltipStyle } from "@/lib/chart-theme";
import { api, ApiError } from "@/lib/api";
import { MODEL_LABEL, useModelStatus } from "@/lib/forecast";
import type { AnomalyModelMeta, DetectionScore, ForecastModelMeta, HorizonMetrics, ModelStatus, RegressionScore } from "@/lib/types";
import { cn } from "@/lib/utils";

const BASELINE_LABEL: Record<string, string> = {
  persistence: "Persistence",
  same_time_yesterday: "Same time yesterday",
  ridge: "Ridge regression",
  seasonal_naive_24h: "Seasonal naive (24 h)",
  seasonal_naive_7d: "Seasonal naive (7 d)",
};

function useMeta<T>(kind: string) {
  const [state, setState] = useState<{ meta: T | null; error: string | null; loading: boolean }>({ meta: null, error: null, loading: true });
  useEffect(() => {
    let alive = true;
    api.modelMeta(kind).then(
      (m) => alive && setState({ meta: m as T, error: null, loading: false }),
      (e: ApiError) => alive && setState({ meta: null, error: e.status === 404 ? "not-served" : e.message, loading: false }),
    );
    return () => {
      alive = false;
    };
  }, [kind]);
  return state;
}

function Card({ children, className }: { children: React.ReactNode; className?: string }) {
  return <section className={cn("panel rounded-xl p-5", className)}>{children}</section>;
}

function fmtErr(kind: string, v: number | undefined | null) {
  if (v == null || Number.isNaN(v)) return "—";
  return kind === "traffic" ? v.toFixed(4) : v.toFixed(3);
}

function Improvement({ pct }: { pct: number }) {
  const good = pct > 0;
  return <span className={cn("font-mono num", good ? "text-ok" : "text-crit")}>{pct > 0 ? "+" : ""}{pct.toFixed(1)}%</span>;
}

function MetricsTable({ meta, split }: { meta: ForecastModelMeta; split: "test" | "holdout" }) {
  const by = meta.metrics[split];
  const horizons = Object.keys(by).filter((h) => meta.kind !== "energy" || ["1", "3", "6", "12", "18", "24"].includes(h));
  const u = meta.horizon_unit === "h" ? "h" : "min";
  return (
    <table className="w-full text-xs [&_td]:pr-3 [&_th]:pr-3">
      <thead>
        <tr className="text-left text-muted-foreground">
          <th className="label-caps pb-2 font-normal">Horizon</th>
          <th className="label-caps pb-2 font-normal text-brand/80">XGBoost</th>
          {meta.baselines.map((b) => (
            <th key={b} className="label-caps pb-2 font-normal">{BASELINE_LABEL[b] ?? b}</th>
          ))}
          <th className="label-caps pb-2 font-normal">vs best baseline</th>
        </tr>
      </thead>
      <tbody>
        {horizons.map((h) => {
          const e = by[h] as HorizonMetrics;
          return (
            <tr key={h} className="border-t border-border/60">
              <td className="py-1.5 font-mono">{h} {u}</td>
              <td className="font-mono text-foreground num">{fmtErr(meta.kind, e.model.mae)}</td>
              {meta.baselines.map((b) => (
                <td key={b} className={cn("font-mono text-muted-foreground num", e.best_baseline === b && "text-foreground/80 underline decoration-dotted")}>
                  {fmtErr(meta.kind, (e[b] as RegressionScore | undefined)?.mae)}
                </td>
              ))}
              <td><Improvement pct={e.mae_improvement_pct} /></td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function HardSubsets({ meta }: { meta: ForecastModelMeta }) {
  const by = meta.metrics.test;
  return (
    <table className="w-full text-xs [&_td]:pr-3 [&_th]:pr-3">
      <thead>
        <tr className="text-left text-muted-foreground">
          <th className="label-caps pb-2 font-normal">Horizon</th>
          <th className="label-caps pb-2 font-normal">Subset</th>
          <th className="label-caps pb-2 font-normal text-brand/80">XGBoost</th>
          {meta.baselines.map((b) => (
            <th key={b} className="label-caps pb-2 font-normal">{BASELINE_LABEL[b] ?? b}</th>
          ))}
        </tr>
      </thead>
      <tbody>
        {Object.entries(by).flatMap(([h, e]) =>
          (["peak", "changing"] as const).map((sub) => (
            <tr key={h + sub} className="border-t border-border/60">
              <td className="py-1.5 font-mono">{h} min</td>
              <td className="text-muted-foreground">{sub === "peak" ? "weekday rush hours" : "congestion changes > 0.1"}</td>
              <td className="font-mono num">{fmtErr("traffic", e[sub]?.model)}</td>
              {meta.baselines.map((b) => (
                <td key={b} className="font-mono text-muted-foreground num">{fmtErr("traffic", e[sub]?.[b])}</td>
              ))}
            </tr>
          )),
        )}
      </tbody>
    </table>
  );
}

function Importance({ meta }: { meta: ForecastModelMeta }) {
  const key = Object.keys(meta.feature_importance)[meta.kind === "traffic" ? 1 : 0];
  const data = meta.feature_importance[key].slice(0, 10).map((f) => ({ name: f.feature, share: +(f.share * 100).toFixed(1) }));
  return (
    <div>
      <div className="label-caps mb-1">Feature importance (gain share{meta.kind === "traffic" ? ", 30-min model" : ""})</div>
      <div className="h-[230px]">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={data} layout="vertical" margin={{ top: 0, right: 16, bottom: 0, left: 8 }}>
            <XAxis type="number" hide />
            <YAxis type="category" dataKey="name" width={130} tick={{ fontSize: 10.5, fill: "#a1a1aa", fontFamily: "var(--font-geist-mono)" }} axisLine={false} tickLine={false} />
            <Tooltip cursor={cursorFill} contentStyle={tooltipStyle} formatter={(v) => [`${v}%`, "gain share"]} />
            <Bar dataKey="share" fill="#4c8dff" fillOpacity={0.75} radius={[0, 3, 3, 0]} isAnimationActive={false} />
          </BarChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}

function DataFacts({ meta }: { meta: ForecastModelMeta | AnomalyModelMeta }) {
  const d = meta.data as ForecastModelMeta["data"];
  return (
    <div className="flex flex-wrap gap-x-5 gap-y-1 text-[12px] text-muted-foreground">
      <span className="flex items-center gap-1.5"><Database className="size-3" /> train cities: seeds {d.train_seeds.join(", ")}</span>
      <span>held-out city: seed {d.holdout_seeds.join(", ")}</span>
      {d.split_days && (
        <span>
          days {d.split_days.train[0]}–{d.split_days.train[1] - 1} train · {d.split_days.val[0]}–{d.split_days.val[1] - 1} validation · {d.split_days.test[0]}–{d.split_days.test[1] - 1} test
        </span>
      )}
      <span>live city (seed 42) never used in training</span>
      <span>trained in {meta.training_seconds}s</span>
    </div>
  );
}

function Live({ status, kind }: { status: ModelStatus | undefined; kind: string }) {
  if (!status) return null;
  const pts = status.live_accuracy.filter((a) => kind !== "energy" || [1, 6, 24].includes(a.horizon));
  if (pts.length === 0) return <p className="text-[12px] text-muted-foreground">Live accuracy: collecting on this run…</p>;
  return (
    <p className="text-[12px] text-muted-foreground">
      Live on this run (last 6 sim-h):{" "}
      {pts.map((a) => (
        <span key={a.horizon} className="mr-3 font-mono">
          +{a.horizon}{kind === "energy" ? "h" : "m"} {fmtErr(kind, a.model_mae)}
          {a.baseline_mae != null && <span className={a.model_mae < a.baseline_mae ? "text-ok" : "text-warn"}> vs {fmtErr(kind, a.baseline_mae)}</span>}
        </span>
      ))}
    </p>
  );
}

function ForecastCard({ kind, status }: { kind: "traffic" | "energy" | "pm25"; status: ModelStatus | undefined }) {
  const { meta, error, loading } = useMeta<ForecastModelMeta>(kind);
  const [split, setSplit] = useState<"test" | "holdout">("test");
  if (loading) return <Card><div className="h-40 animate-pulse rounded-lg bg-raised" /></Card>;
  if (!meta)
    return (
      <Card>
        <h2 className="text-base font-semibold">{MODEL_LABEL[kind]}</h2>
        <p className="mt-2 text-xs text-muted-foreground">
          {error === "not-served" ? "No service is serving this model. Train it (nexus-ml train) and start the inference service (nexus-ml serve)." : error}
        </p>
      </Card>
    );
  const avg = Object.values(meta.metrics[split]).map((e) => e.mae_improvement_pct);
  const unit = { traffic: "congestion index", energy: "MW", pm25: "µg/m³" }[kind];
  return (
    <Card>
      <div className="flex flex-wrap items-start gap-3">
        <div className="flex-1">
          <div className="eyebrow">{MODEL_LABEL[kind]}</div>
          <h2 className="text-base font-semibold">{meta.description}</h2>
          <p className="font-mono text-[12px] text-muted-foreground">{meta.version} · XGBoost {meta.library_versions?.xgboost} · {meta.features.length} features</p>
        </div>
        <div className="rounded-lg border border-border bg-raised px-3 py-2 text-right">
          <div className="label-caps">MAE vs best baseline ({split})</div>
          <div className="font-mono text-lg"><Improvement pct={avg.reduce((a, b) => a + b, 0) / avg.length} /></div>
        </div>
      </div>
      <div className="mt-3"><DataFacts meta={meta} /></div>
      <div className="mt-4 grid grid-cols-1 gap-5 lg:grid-cols-5">
        <div className="space-y-3 lg:col-span-3">
          <div className="flex items-center gap-2">
            <span className="label-caps flex-1">Mean absolute error ({unit}), lower is better</span>
            <div className="flex rounded-md border border-border p-0.5 text-[12px]">
              {(["test", "holdout"] as const).map((s) => (
                <button key={s} type="button" onClick={() => setSplit(s)} className={cn("rounded px-2 py-0.5", split === s ? "bg-[#2a2a2e] text-foreground" : "text-muted-foreground hover:text-foreground")}>
                  {s === "test" ? "Test days" : "Unseen city"}
                </button>
              ))}
            </div>
          </div>
          <MetricsTable meta={meta} split={split} />
          {kind === "traffic" && split === "test" && (
            <>
              <div className="label-caps pt-2">Where it matters (test days)</div>
              <HardSubsets meta={meta} />
            </>
          )}
          <Live status={status} kind={kind} />
        </div>
        <div className="lg:col-span-2">
          <Importance meta={meta} />
        </div>
      </div>
    </Card>
  );
}

function Score({ s }: { s: DetectionScore }) {
  const f = (v: number | null) => (v == null || Number.isNaN(v) ? "—" : v.toFixed(2));
  return (
    <span className="font-mono">
      {f(s.f1)} <span className="text-muted-foreground">({f(s.precision)} / {f(s.recall)})</span>
    </span>
  );
}

function AnomalyCard() {
  const { meta, error, loading } = useMeta<AnomalyModelMeta>("anomaly");
  if (loading) return <Card><div className="h-40 animate-pulse rounded-lg bg-raised" /></Card>;
  if (!meta)
    return (
      <Card>
        <h2 className="text-base font-semibold">Anomaly detector</h2>
        <p className="mt-2 text-xs text-muted-foreground">{error === "not-served" ? "No service is serving the anomaly model." : error}</p>
      </Card>
    );
  const names: Record<string, string> = { traffic: "Roads — incidents", energy: "Districts — demand surges", air: "Stations — faults & emission releases" };
  const ruleWins = Object.entries(meta.metrics).filter(([, m]) => m.test.rule.f1 > m.test.model.f1 + 0.02).map(([d]) => d);
  return (
    <Card>
      <div className="eyebrow">Anomaly detector</div>
      <h2 className="text-base font-semibold">{meta.description}</h2>
      <p className="font-mono text-[12px] text-muted-foreground">{meta.version}</p>
      <div className="mt-3"><DataFacts meta={meta} /></div>
      {ruleWins.length > 0 && (
        <div className="mt-3 flex gap-2 rounded-lg border border-warn/30 bg-warn/5 px-3 py-2 text-xs">
          <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-warn" />
          <p>
            On the test days, the hand-written rule baseline beats Isolation Forest for{" "}
            <span className="font-medium">{ruleWins.map((d) => names[d].split(" — ")[0].toLowerCase()).join(" and ")}</span>.
            The rules stay the primary alerts; the forest is shown as a second opinion and catches episodes the rules miss.
          </p>
        </div>
      )}
      <table className="mt-4 w-full text-xs [&_td]:pr-3 [&_th]:pr-3">
        <thead>
          <tr className="text-left text-muted-foreground">
            <th className="label-caps pb-2 font-normal">Domain</th>
            <th className="label-caps pb-2 font-normal">Split</th>
            <th className="label-caps pb-2 font-normal text-brand/80">Isolation Forest F1 (precision / recall)</th>
            <th className="label-caps pb-2 font-normal">Rule baseline F1</th>
            <th className="label-caps pb-2 font-normal">Episodes caught (forest / rule)</th>
          </tr>
        </thead>
        <tbody>
          {Object.entries(meta.metrics).flatMap(([d, m]) =>
            (["val", "test", "holdout"] as const).map((split) => {
              const e = m[split];
              const ev = split === "val" ? null : m[split].events;
              const win = e.model.f1 >= e.rule.f1;
              return (
                <tr key={d + split} className="border-t border-border/60">
                  <td className="py-1.5">{split === "val" ? names[d] : ""}</td>
                  <td className="text-muted-foreground">{split === "val" ? "validation" : split === "test" ? "test days" : "unseen city"}</td>
                  <td className={cn(win ? "text-ok" : "text-foreground")}><Score s={e.model} /></td>
                  <td className={cn(!win ? "text-warn" : "text-muted-foreground")}><Score s={e.rule} /></td>
                  <td className="font-mono">{ev ? `${ev.model.detected}/${ev.model.events} · ${ev.rule.detected}/${ev.rule.events}` : "—"}</td>
                </tr>
              );
            }),
          )}
        </tbody>
      </table>
      <p className="mt-3 text-[12px] leading-relaxed text-muted-foreground">{meta.notes} Alert thresholds for both detectors are tuned on validation days only.</p>
    </Card>
  );
}

export function ModelLab() {
  const { status } = useModelStatus();
  const by = Object.fromEntries((status ?? []).map((s) => [s.kind, s]));
  return (
    <div className="mx-auto max-w-[1400px] space-y-5 px-4 py-6 sm:px-6">
      <div>
        <div className="eyebrow flex items-center gap-1.5"><Microscope className="size-3.5" /> Model lab</div>
        <h1 className="mt-1 text-[22px] font-semibold tracking-[-0.01em]">How good are the models — measured, not claimed</h1>
        <p className="mt-1.5 max-w-3xl text-[13.5px] leading-relaxed text-muted-foreground">
          <FlaskConical className="mr-1 inline size-3" />
          Every number below comes from the training metadata of the model version currently being served. Models train on three
          simulated cities, are early-stopped on validation days, and are scored on later test days and on a fourth city they never
          saw. These scores show how well a model learned the simulator — not real-world forecasting skill.
        </p>
        <p className="mt-2 flex items-center gap-1.5 text-[12px] text-muted-foreground">
          <CheckCircle2 className="size-3 text-ok" /> green = better than the best baseline · red = worse
        </p>
      </div>
      <ForecastCard kind="traffic" status={by.traffic} />
      <ForecastCard kind="energy" status={by.energy} />
      <ForecastCard kind="pm25" status={by.pm25} />
      <AnomalyCard />
    </div>
  );
}
