export type VectorClock = Record<string, number>;

export type Ordering = "before" | "after" | "equal" | "concurrent";

export function tick(clock: VectorClock, replicaId: string): VectorClock {
  return { ...clock, [replicaId]: (clock[replicaId] ?? 0) + 1 };
}

export function mergeClocks(a: VectorClock, b: VectorClock): VectorClock {
  const out: VectorClock = { ...a };
  for (const [key, value] of Object.entries(b)) {
    out[key] = Math.max(out[key] ?? 0, value);
  }
  return out;
}

export function compare(a: VectorClock, b: VectorClock): Ordering {
  let less = false;
  let greater = false;
  for (const key of new Set([...Object.keys(a), ...Object.keys(b)])) {
    const x = a[key] ?? 0;
    const y = b[key] ?? 0;
    if (x < y) less = true;
    else if (x > y) greater = true;
  }
  if (less && greater) return "concurrent";
  if (less) return "before";
  if (greater) return "after";
  return "equal";
}

export function causallyReady(
  incoming: VectorClock,
  local: VectorClock,
  origin: string,
): boolean {
  if ((incoming[origin] ?? 0) !== (local[origin] ?? 0) + 1) return false;
  for (const [key, value] of Object.entries(incoming)) {
    if (key !== origin && value > (local[key] ?? 0)) return false;
  }
  return true;
}
