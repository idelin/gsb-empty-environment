import { Operation, applyOp } from '../src/operation';
import { serialReference } from '../src/reference';
import { OptimisticStore } from '../src/store';
import {
  assertFailuresEqual,
  assertStateEqual,
  test,
} from './harness';

/** Deterministic PRNG (mulberry32) so failures are reproducible. */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomOps(rng: () => number, count: number): Operation[] {
  const ids = ['a', 'b', 'c', 'd', 'e', 'f'];
  const ops: Operation[] = [];
  for (let i = 0; i < count; i += 1) {
    const opId = `op${i}`;
    const target = ids[Math.floor(rng() * ids.length)]!;
    const kind = rng();
    if (kind < 0.4) {
      ops.push({ opId, kind: 'create', target, value: i });
    } else if (kind < 0.8) {
      ops.push({ opId, kind: 'update', target, value: i });
    } else {
      ops.push({ opId, kind: 'delete', target });
    }
  }
  return ops;
}

/**
 * Simulated server: processes ops serially in submission order. Ops that
 * cannot apply are always rejected; applicable ops are rejected randomly
 * 20% of the time. Returns opId -> accepted.
 */
function simulateServer(
  initial: ReadonlyMap<string, number>,
  ops: readonly Operation[],
  rng: () => number,
): Map<string, boolean> {
  const verdicts = new Map<string, boolean>();
  const state = new Map(initial);
  for (const op of ops) {
    const probe = new Map(state);
    const applicable = applyOp(probe, op) === 'applied';
    const accepted = applicable && rng() >= 0.2;
    verdicts.set(op.opId, accepted);
    if (accepted) applyOp(state, op);
  }
  return verdicts;
}

function shuffled<T>(items: readonly T[], rng: () => number): T[] {
  const out = [...items];
  for (let i = out.length - 1; i > 0; i -= 1) {
    const j = Math.floor(rng() * (i + 1));
    [out[i], out[j]] = [out[j]!, out[i]!];
  }
  return out;
}

for (const seed of [1, 7, 42, 1337, 20261007]) {
  test(`fuzz seed ${seed}: out-of-order receipts match serial reference`, () => {
    const rng = mulberry32(seed);
    const initial = new Map<string, number>([['a', 0]]);
    const ops = randomOps(rng, 150);
    const verdicts = simulateServer(initial, ops, rng);

    const store = new OptimisticStore(initial);
    for (const op of ops) store.submit(op);

    // Before any receipt: everything pending, all applied optimistically.
    let ref = serialReference(initial, ops, new Map());
    assertStateEqual(store.getState(), ref.state, `seed ${seed} initial view`);
    assertFailuresEqual(store.getFailures(), ref.failures, `seed ${seed} initial failures`);

    // Deliver every receipt in random (out-of-submission) order, checking
    // the full invariant after each single receipt.
    const known = new Map<string, boolean>();
    for (const opId of shuffled(ops.map((o) => o.opId), rng)) {
      const accepted = verdicts.get(opId)!;
      store.handleReceipt({ opId, accepted });
      known.set(opId, accepted);
      ref = serialReference(initial, ops, known);
      assertStateEqual(
        store.getState(),
        ref.state,
        `seed ${seed} state after receipt of ${opId}`,
      );
      assertFailuresEqual(
        store.getFailures(),
        ref.failures,
        `seed ${seed} failures after receipt of ${opId}`,
      );
    }
  });
}

test('fuzz: different receipt orders of the same run converge identically', () => {
  const rng = mulberry32(99);
  const ops = randomOps(rng, 80);
  const verdicts = simulateServer(new Map(), ops, rng);

  const finalStates: string[] = [];
  for (const orderSeed of [1, 2, 3]) {
    const orderRng = mulberry32(orderSeed);
    const store = new OptimisticStore();
    for (const op of ops) store.submit(op);
    for (const opId of shuffled(ops.map((o) => o.opId), orderRng)) {
      store.handleReceipt({ opId, accepted: verdicts.get(opId)! });
    }
    finalStates.push(
      JSON.stringify([...store.getState().entries()].sort()),
    );
  }
  if (finalStates[0] !== finalStates[1] || finalStates[1] !== finalStates[2]) {
    throw new Error(`receipt order changed the outcome: ${finalStates.join(' vs ')}`);
  }
});
