/**
 * Serial reference model: the source of truth for verification.
 *
 * The "server" is modelled as applying ops strictly in submission order.
 * An op is accepted iff it applies cleanly on top of all previously
 * accepted ops and is not randomly rejected (random rejection stands in
 * for server-side validation failures unrelated to state).
 *
 * Whatever the client does with out-of-order receipts, its final state
 * must equal the serial model's state.
 */
import { applyOp, applies, type Operation } from '../src/store.ts';

export interface SimulatedOp<V> {
  readonly op: Operation<V>;
  /** True if the server rejects this op even though it would apply. */
  readonly randomlyRejected: boolean;
}

export interface Receipt {
  readonly id: string;
  readonly accepted: boolean;
}

export interface SerialOutcome<V> {
  /** Final state after serially applying every accepted op. */
  readonly state: Map<string, V>;
  /** One receipt per submitted op, in submission order. */
  readonly receipts: Receipt[];
  /** Ids of ops the serial model applied. */
  readonly acceptedIds: ReadonlySet<string>;
}

export function runSerialModel<V>(
  initial: ReadonlyMap<string, V>,
  simOps: readonly SimulatedOp<V>[],
): SerialOutcome<V> {
  const state = new Map(initial);
  const receipts: Receipt[] = [];
  const acceptedIds = new Set<string>();
  for (const { op, randomlyRejected } of simOps) {
    const accepted = !randomlyRejected && applies(state, op);
    if (accepted) {
      applyOp(state, op);
      acceptedIds.add(op.id);
    }
    receipts.push({ id: op.id, accepted });
  }
  return { state, receipts, acceptedIds };
}

/**
 * Expected client-visible state midway through receipt processing:
 * serially walk the submission order, skip ops already known to be
 * rejected, skip ops that fail to apply (dependency failures), and apply
 * everything else optimistically.
 */
export function expectedVisibleState<V>(
  initial: ReadonlyMap<string, V>,
  simOps: readonly SimulatedOp<V>[],
  rejectedSoFar: ReadonlySet<string>,
): Map<string, V> {
  const state = new Map(initial);
  for (const { op } of simOps) {
    if (rejectedSoFar.has(op.id)) continue;
    if (applies(state, op)) applyOp(state, op);
  }
  return state;
}

/** Deterministic PRNG (mulberry32) so failures are reproducible. */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function shuffled<T>(items: readonly T[], rand: () => number): T[] {
  const copy = [...items];
  for (let i = copy.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [copy[i], copy[j]] = [copy[j], copy[i]];
  }
  return copy;
}

export function mapsEqual<V>(a: ReadonlyMap<string, V>, b: ReadonlyMap<string, V>): boolean {
  if (a.size !== b.size) return false;
  for (const [key, value] of a) {
    if (!b.has(key)) return false;
    const other = b.get(key);
    if (JSON.stringify(value) !== JSON.stringify(other)) return false;
  }
  return true;
}
