/** 向量时钟：作为操作的因果版本。 */
export type VectorClock = Record<string, number>;

export function tick(clock: VectorClock, replicaId: string): VectorClock {
  return { ...clock, [replicaId]: (clock[replicaId] ?? 0) + 1 };
}

export function merge(a: VectorClock, b: VectorClock): VectorClock {
  const out: VectorClock = { ...a };
  for (const [k, v] of Object.entries(b)) out[k] = Math.max(out[k] ?? 0, v);
  return out;
}

/** a 是否因果支配 b（a 知道 b 所知道的一切，且至少多一条）。 */
export function dominates(a: VectorClock, b: VectorClock): boolean {
  let strictlyGreater = false;
  for (const k of new Set([...Object.keys(a), ...Object.keys(b)])) {
    const av = a[k] ?? 0;
    const bv = b[k] ?? 0;
    if (av < bv) return false;
    if (av > bv) strictlyGreater = true;
  }
  return strictlyGreater;
}

export function compare(a: VectorClock, b: VectorClock): 'dominates' | 'dominated' | 'equal' | 'concurrent' {
  if (dominates(a, b)) return 'dominates';
  if (dominates(b, a)) return 'dominated';
  const ak = Object.keys(a), bk = Object.keys(b);
  if (ak.length === bk.length && ak.every((k) => (a[k] ?? 0) === (b[k] ?? 0))) return 'equal';
  return 'concurrent';
}
