export type NodeId = string;

/** A vector clock maps each node id to the number of events observed from it. */
export type VectorClock = Record<NodeId, number>;

export function tick(clock: VectorClock, id: NodeId): VectorClock {
  return { ...clock, [id]: (clock[id] ?? 0) + 1 };
}

export function merge(a: VectorClock, b: VectorClock): VectorClock {
  const out: VectorClock = { ...a };
  for (const k of Object.keys(b)) out[k] = Math.max(out[k] ?? 0, b[k]);
  return out;
}

/** True when every entry of `a` is <= the matching entry of `b`. */
export function lessOrEqual(a: VectorClock, b: VectorClock): boolean {
  return Object.keys(a).every((k) => a[k] <= (b[k] ?? 0));
}

/**
 * Standard causal-broadcast delivery rule for a message sent by `sender`
 * carrying vector timestamp `msg`, evaluated against the receiver's clock.
 *
 * 1. It is the very next message expected from `sender` (no gap per-sender).
 * 2. Every event the sender had observed before sending has already been
 *    delivered locally (all causal dependencies satisfied).
 */
export function isDeliverable(
  msg: VectorClock,
  sender: NodeId,
  local: VectorClock,
): boolean {
  if (msg[sender] !== (local[sender] ?? 0) + 1) return false;
  return Object.keys(msg).every(
    (k) => k === sender || msg[k] <= (local[k] ?? 0),
  );
}
