"use client";

import { Area, AreaChart, CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { PALETTE, axisLine, axisTick, gridStroke, tooltipStyle } from "@/lib/chart-theme";
import type { InfraSeries } from "@/lib/types";

export { PALETTE };


const axisStyle = axisTick;
const clock = (t: number) => new Date(t * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });

export interface Line_ {
  key: string;
  label: string;
  color: string;
  series?: InfraSeries[];
}

/** Merge Prometheus series into chart rows keyed by timestamp. */
function rows(lines: Line_[]) {
  const byT = new Map<number, Record<string, number>>();
  for (const l of lines)
    for (const s of l.series ?? [])
      for (const [t, v] of s.points) {
        const r = byT.get(t) ?? { t };
        r[l.key] = (r[l.key] ?? 0) + v;
        byT.set(t, r);
      }
  return [...byT.values()].sort((a, b) => a.t - b.t);
}

/** Wall-clock time series (infrastructure metrics are real time, not simulated time). */
export function MetricChart({ lines, height = 160, format = (v: number) => v.toFixed(1), stacked = false, step = false }: {
  lines: Line_[];
  height?: number;
  format?: (v: number) => string;
  stacked?: boolean;
  step?: boolean;
}) {
  const data = rows(lines);
  if (data.length === 0) {
    return <div className="grid place-items-center text-xs text-muted-foreground" style={{ height }}>No samples yet</div>;
  }
  const common = {
    data,
    margin: { top: 6, right: 8, bottom: 0, left: 0 },
  };
  const axes = (
    <>
      <CartesianGrid stroke={gridStroke} vertical={false} />
      <XAxis dataKey="t" tickFormatter={clock} tick={axisStyle} stroke={axisLine} tickLine={false} minTickGap={40} />
      <YAxis tick={axisStyle} width={44} stroke={axisLine} tickLine={false} axisLine={false} tickFormatter={format} allowDecimals />
      <Tooltip
        contentStyle={tooltipStyle}
        labelFormatter={(t) => clock(Number(t))}
        formatter={(v, name) => [format(Number(v)), lines.find((l) => l.key === name)?.label ?? String(name)]}
      />
    </>
  );
  return (
    <ResponsiveContainer width="100%" height={height}>
      {stacked ? (
        <AreaChart {...common}>
          {axes}
          {lines.map((l) => (
            <Area key={l.key} dataKey={l.key} name={l.key} stackId="a" stroke={l.color} fill={l.color} fillOpacity={0.25}
              strokeWidth={1.5} isAnimationActive={false} type="monotone" />
          ))}
        </AreaChart>
      ) : (
        <LineChart {...common}>
          {axes}
          {lines.map((l) => (
            <Line key={l.key} dataKey={l.key} name={l.key} stroke={l.color} strokeWidth={2} dot={false}
              isAnimationActive={false} type={step ? "stepAfter" : "monotone"} />
          ))}
        </LineChart>
      )}
    </ResponsiveContainer>
  );
}

/** One line per label value (e.g. per component). */
export function splitBy(series: InfraSeries[] | undefined, label: string): Line_[] {
  return (series ?? [])
    .map((s) => s.labels[label] ?? "other")
    .filter((v, i, a) => a.indexOf(v) === i)
    .sort()
    .map((v, i) => ({ key: v, label: v, color: PALETTE[i % PALETTE.length], series: (series ?? []).filter((s) => (s.labels[label] ?? "other") === v) }));
}
