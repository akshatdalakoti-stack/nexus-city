// Formatting helpers and colour scales shared by charts and the map.

export const fmt = {
  int: (v: number) => Math.round(v).toLocaleString("en-US"),
  num: (v: number, digits = 1) => v.toLocaleString("en-US", { minimumFractionDigits: digits, maximumFractionDigits: digits }),
  pct: (v: number, digits = 0) => `${(v * 100).toFixed(digits)}%`,
  compact: (v: number) => Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 }).format(v),
};

const DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

/** Simulated times are city-local and carried as UTC in the payload. */
export function simClock(iso: string | null | undefined) {
  if (!iso) return { time: "--:--", seconds: "--", day: "---", date: "----" };
  const d = new Date(iso);
  const p = (n: number) => String(n).padStart(2, "0");
  return {
    time: `${p(d.getUTCHours())}:${p(d.getUTCMinutes())}`,
    seconds: p(d.getUTCSeconds()),
    day: DAYS[d.getUTCDay()],
    date: `${d.getUTCFullYear()}-${p(d.getUTCMonth() + 1)}-${p(d.getUTCDate())}`,
  };
}

export function hhmm(iso: string) {
  const d = new Date(iso);
  return `${String(d.getUTCHours()).padStart(2, "0")}:${String(d.getUTCMinutes()).padStart(2, "0")}`;
}

/**
 * Congestion 0..1 → colour, on the scale people know from Google and Apple
 * Maps traffic: green (free-flowing) → yellow → orange → red → dark red (stopped).
 */
export const congestionStops: [number, string][] = [
  [0, "#2fa36b"],
  [0.2, "#e3c13b"],
  [0.4, "#f28c38"],
  [0.6, "#f0565b"],
  [0.8, "#a3242b"],
];

/** US EPA PM2.5 breakpoints (24h) used only as a colour guide. */
export const pmStops: [number, string, string][] = [
  [0, "#3ecf8e", "Good"],
  [12.1, "#f2c037", "Moderate"],
  [35.5, "#f28c38", "Unhealthy (sensitive)"],
  [55.5, "#f0565b", "Unhealthy"],
  [150.5, "#c026d3", "Very unhealthy"],
];

export function pmColor(v: number | null | undefined) {
  if (v == null) return "#52525b";
  let c = pmStops[0][1];
  for (const [t, col] of pmStops) if (v >= t) c = col;
  return c;
}

export function pmLabel(v: number | null | undefined) {
  if (v == null) return "No reading";
  let l = pmStops[0][2];
  for (const [t, , lab] of pmStops) if (v >= t) l = lab;
  return l;
}

export function congestionColor(v: number) {
  let c = congestionStops[0][1];
  for (const [t, col] of congestionStops) if (v >= t) c = col;
  return c;
}

export function utilColor(u: number) {
  if (u >= 1) return "#f0565b";
  if (u >= 0.9) return "#f28c38";
  if (u >= 0.75) return "#f2c037";
  return "#3ecf8e";
}

export const severityColor: Record<string, string> = {
  info: "#7fb0ff",
  warning: "#f5a524",
  critical: "#f0565b",
};

export const districtTypeColor: Record<string, string> = {
  residential: "#4c8dff",
  commercial: "#2bb5a5",
  industrial: "#f5a524",
  mixed: "#a78bfa",
};
