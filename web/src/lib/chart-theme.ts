// One look for every chart: quiet axes and grid, a solid tooltip, and a
// categorical palette whose first colour is the brand blue.

export const SERIES = {
  blue: "#4c8dff",
  teal: "#2bb5a5",
  violet: "#a594f9",
  amber: "#f5a524",
  green: "#3ecf8e",
  red: "#f0565b",
  pink: "#f07a95",
  grey: "#a1a1aa",
  yellow: "#f2c037",
  orange: "#f28c38",
} as const;

/** Distinct colours in a fixed order, for series without a semantic colour. */
export const PALETTE = [
  SERIES.blue, SERIES.teal, SERIES.violet, SERIES.amber, SERIES.green,
  SERIES.red, SERIES.pink, SERIES.grey, SERIES.yellow, SERIES.orange,
];

export const axisTick = { fontSize: 11, fill: "#8f8f97", fontFamily: "var(--font-geist-sans)" };
export const axisLine = "#2a2a2e";
export const gridStroke = "rgb(255 255 255 / 0.06)";
export const tooltipStyle = {
  background: "#161618",
  border: "1px solid #2a2a2e",
  borderRadius: 8,
  fontSize: 12,
  color: "#ececee",
  boxShadow: "0 8px 24px -10px rgb(0 0 0 / 0.8)",
};
export const cursorFill = { fill: "rgb(255 255 255 / 0.04)" };
