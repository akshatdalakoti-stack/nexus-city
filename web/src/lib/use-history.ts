"use client";

import { useEffect, useRef, useState } from "react";
import { api } from "./api";
import { useLive } from "./store";
import type { HistoryPoint } from "./types";

/**
 * Fetches an entity's time series from the gateway and refreshes it as new
 * telemetry arrives (at most every `minGapMs`, so 60× speed stays cheap).
 * Responses for an entity that is no longer selected are discarded.
 */
export function useHistory<T>(kind: string | null, id: string | null, minutes = 180, minGapMs = 2500) {
  const seq = useLive((s) => s.telemetrySeq);
  const runId = useLive((s) => s.runId);
  const key = `${runId}|${kind}|${id}|${minutes}`;
  const [state, setState] = useState<{ key: string; points: HistoryPoint<T>[] | null; error: string | null }>({
    key: "",
    points: null,
    error: null,
  });
  const currentKey = useRef(key);
  const lastFetch = useRef({ key: "", at: 0 });

  useEffect(() => {
    currentKey.current = key;
  }, [key]);

  useEffect(() => {
    if (!kind || !id) return;
    const now = Date.now();
    if (lastFetch.current.key === key && now - lastFetch.current.at < minGapMs) return;
    lastFetch.current = { key, at: now };
    api.history<T>(kind, id, minutes).then(
      (r) => currentKey.current === key && setState({ key, points: r.points, error: null }),
      (e: Error) => currentKey.current === key && setState({ key, points: null, error: e.message }),
    );
  }, [key, seq, kind, id, minutes, minGapMs]);

  const fresh = state.key === key;
  return { points: fresh ? state.points : null, error: fresh ? state.error : null, loading: !fresh };
}
