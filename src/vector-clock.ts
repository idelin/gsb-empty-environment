export type VectorClock = Record<string, number>;

export type Ordering = "equal" | "before" | "after" | "concurrent";

/** Return a new clock with the entry for `id` incremented. */
export function tick(clock: VectorClock, id: string): VectorClock {
  return { ...clock, [id]: (clock[id] ?? 0) + 1 };
}

/** Component-wise maximum of any number of clocks. */
export function merge(...clocks: VectorClock[]): VectorClock {
  const out: VectorClock = {};
  for (const clock of clocks) {
    for (const [id, counter] of Object.entries(clock)) {
      if (counter > (out[id] ?? 0)) out[id] = counter;
    }
  }
  return out;
}

/** Causal ordering between two clocks. */
export function compare(a: VectorClock, b: VectorClock): Ordering {
  let aGreater = false;
  let bGreater = false;
  for (const id of new Set([...Object.keys(a), ...Object.keys(b)])) {
    const av = a[id] ?? 0;
    const bv = b[id] ?? 0;
    if (av > bv) aGreater = true;
    else if (bv > av) bGreater = true;
    if (aGreater && bGreater) return "concurrent";
  }
  if (aGreater) return "after";
  if (bGreater) return "before";
  return "equal";
}

/** True when `a` strictly covers `b` (a happened-after b). */
export function dominates(a: VectorClock, b: VectorClock): boolean {
  return compare(a, b) === "after";
}

/** Stable string form, used for deterministic snapshots and tests. */
export function clockKey(clock: VectorClock): string {
  return Object.keys(clock)
    .sort()
    .map((id) => `${id}:${clock[id]}`)
    .join(",");
}
