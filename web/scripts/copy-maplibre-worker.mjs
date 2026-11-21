// Copies MapLibre's web worker into public/ so the browser can load it from
// a stable URL (see setWorkerUrl in src/components/map/city-map.tsx).
import { copyFileSync, mkdirSync } from "node:fs";
mkdirSync("public/vendor", { recursive: true });
copyFileSync("node_modules/maplibre-gl/dist/maplibre-gl-worker.mjs", "public/vendor/maplibre-gl-worker.mjs");
