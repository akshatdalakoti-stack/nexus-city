"use client";

import { useEffect, useRef, useState } from "react";

/**
 * Throttles `value`: at most one release per `ms`. The first change after a
 * release passes through immediately; changes in between are dropped and the
 * newest one is committed at the slot, so the value never lags more than `ms`
 * behind the source (a leading + trailing throttle, not a debounce).
 *
 * The bar charts and districts table on the Overview consume this; the KPI
 * strip reads the live snapshot directly.
 */
export function useThrottled<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  // Newest value seen since the last release; a pending timer commits this one.
  const latest = useRef(value);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    latest.current = value;
    if (Object.is(value, v)) return; // nothing new since the last release
    if (timer.current) return; // a slot is booked; it will commit the latest
    setV(value); // first change goes through immediately
    timer.current = setTimeout(() => {
      timer.current = null;
      setV(latest.current);
    }, ms);
  }, [value, v, ms]);

  // The timer must survive prop churn — clearing it on every value change would
  // reset the countdown each time and turn this into a debounce. Only cancelled
  // when the component unmounts.
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
      timer.current = null;
    },
    [],
  );

  return v;
}
