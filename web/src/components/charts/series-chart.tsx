"use client";

import { memo } from "react";
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Line,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { axisTick, gridStroke, tooltipStyle } from "@/lib/chart-theme";
import { hhmm } from "@/lib/format";

export interface Series {
  key: string;
  label: string;
  color: string;
  type?: "area" | "line";
  axis?: "left" | "right";
  format?: (v: number) => string;
  dashed?: boolean;
}

interface Props {
  data: Record<string, number | string | null>[];
  series: Series[];
  height?: number;
  /** ISO time to mark (e.g. the replay position). */
  marker?: string | null;
  leftDomain?: [number | "auto", number | "auto"];
  rightDomain?: [number | "auto", number | "auto"];
}

const axisStyle = axisTick;

/** Time-series chart styled for the command center. Rows need a `t` ISO field. */
function SeriesChartImpl({ data, series, height = 150, marker, leftDomain, rightDomain }: Props) {
  const hasRight = series.some((s) => s.axis === "right");
  const rows = data.map((d) => ({ ...d, ts: new Date(d.t as string).getTime() }));
  return (
    <div style={{ height }} className="w-full">
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={rows} margin={{ top: 6, right: hasRight ? 0 : 6, bottom: 0, left: 0 }}>
          <defs>
            {series.map((s) => (
              <linearGradient key={s.key} id={`g-${s.key}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={s.color} stopOpacity={0.22} />
                <stop offset="100%" stopColor={s.color} stopOpacity={0} />
              </linearGradient>
            ))}
          </defs>
          <CartesianGrid stroke={gridStroke} vertical={false} />
          <XAxis
            dataKey="ts"
            type="number"
            domain={["dataMin", "dataMax"]}
            scale="time"
            tickFormatter={(v: number) => hhmm(new Date(v).toISOString())}
            tick={axisStyle}
            axisLine={false}
            tickLine={false}
            minTickGap={36}
          />
          <YAxis yAxisId="left" tick={axisStyle} axisLine={false} tickLine={false} width={44} domain={leftDomain ?? ["auto", "auto"]}
            tickFormatter={(v: number) => series.find((s) => (s.axis ?? "left") === "left")?.format?.(v) ?? String(v)} />
          {hasRight && (
            <YAxis yAxisId="right" orientation="right" tick={axisStyle} axisLine={false} tickLine={false} width={40}
              domain={rightDomain ?? ["auto", "auto"]}
              tickFormatter={(v: number) => series.find((s) => s.axis === "right")?.format?.(v) ?? String(v)} />
          )}
          <Tooltip
            contentStyle={tooltipStyle}
            labelStyle={{ color: "#8f8f97" }}
            cursor={{ stroke: "#3f3f46", strokeWidth: 1 }}
            labelFormatter={(v) => `Sim ${hhmm(new Date(Number(v)).toISOString())}`}
            formatter={(value, name) => {
              const s = series.find((x) => x.label === name);
              const num = typeof value === "number" ? value : Number(value);
              return [s?.format ? s.format(num) : String(value), name];
            }}
          />
          {marker && <ReferenceLine yAxisId="left" x={new Date(marker).getTime()} stroke="#ffffff" strokeOpacity={0.6} strokeDasharray="3 3" />}
          {series.map((s) =>
            s.type === "line" ? (
              <Line key={s.key} yAxisId={s.axis ?? "left"} type="monotone" dataKey={s.key} name={s.label} stroke={s.color}
                strokeWidth={1.75} dot={false} isAnimationActive={false} strokeDasharray={s.dashed ? "4 3" : undefined} connectNulls={false} />
            ) : (
              <Area key={s.key} yAxisId={s.axis ?? "left"} type="monotone" dataKey={s.key} name={s.label} stroke={s.color}
                strokeWidth={1.75} fill={`url(#g-${s.key})`} isAnimationActive={false} connectNulls={false} />
            ),
          )}
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  );
}

/**
 * Memoised: live pages re-render on every telemetry message, but a chart only
 * needs to redraw when its data or config changes. Pass stable props (hoisted
 * constants, useMemo'd data) to benefit.
 */
export const SeriesChart = memo(SeriesChartImpl);
