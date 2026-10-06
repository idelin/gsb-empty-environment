import { test } from "node:test";
import assert from "node:assert/strict";
import {
  CounterOp,
  CounterSnapshot,
  IncOp,
  ObservedResetCounter,
  ResetOp,
} from "../src/counter.js";

/** Reference model: value derived directly from op-set semantics. */
function modelValue(ops: readonly CounterOp[]): number {
  const cleared = new Set<string>();
  for (const op of ops) {
    if (op.kind === "reset") for (const id of op.observed) cleared.add(id);
  }
  let total = 0;
  for (const op of ops) {
    if (op.kind === "inc" && !cleared.has(op.id)) total += op.amount;
  }
  return total;
}

function* permutations<T>(items: readonly T[]): Generator<T[]> {
  if (items.length <= 1) {
    yield [...items];
    return;
  }
  for (let i = 0; i < items.length; i++) {
    const rest = [...items.slice(0, i), ...items.slice(i + 1)];
    for (const perm of permutations(rest)) yield [items[i], ...perm];
  }
}

/** Deterministic PRNG (mulberry32) so the property test is reproducible. */
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

function shuffled<T>(items: readonly T[], rng: () => number): T[] {
  const out = [...items];
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(rng() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

test("increments accumulate; value sums all contributions", () => {
  const r = new ObservedResetCounter("r1");
  r.increment();
  r.increment(3);
  r.increment(2);
  assert.equal(r.value(), 6);
  assert.equal(r.size, 3);
});

test("op ids are replica-scoped logical sequences, not wall-clock time", () => {
  const r = new ObservedResetCounter("r1");
  const a = r.increment();
  const b = r.reset();
  const c = r.increment();
  assert.deepEqual(
    [a.id, b.id, c.id],
    ["r1#1", "r1#2", "r1#3"],
  );
  for (const op of [a, b, c]) {
    assert.ok(!("time" in op) && !("timestamp" in op) && !("clock" in op));
  }
});

test("reset clears exactly the observed operations", () => {
  const r = new ObservedResetCounter("r1");
  r.increment(5);
  r.increment(2);
  const reset = r.reset();
  assert.equal(r.value(), 0);
  assert.deepEqual(reset.observed, ["r1#1", "r1#2"]);
  r.increment(4);
  assert.equal(r.value(), 4);
});

test("concurrent (unobserved) increment survives a reset", () => {
  const r1 = new ObservedResetCounter("r1");
  const r2 = new ObservedResetCounter("r2");
  const a = r1.increment(1);
  r2.apply(a); // r2 sees a
  const reset = r2.reset(); // observes only a
  const b = r1.increment(7); // concurrent with the reset
  r1.merge(r2.snapshot());
  r2.apply(b);
  for (const r of [r1, r2]) {
    assert.equal(r.value(), 7);
    assert.ok(r.has(reset.id));
  }
});

test("delayed increment issued before the reset but never observed still counts", () => {
  const r1 = new ObservedResetCounter("r1");
  const r2 = new ObservedResetCounter("r2");
  const a = r1.increment(3);
  r2.apply(a);
  const b = r1.increment(10); // in flight, r2 has not seen it
  const reset = r2.reset(); // observes only a
  r1.apply(reset);
  r2.apply(b); // b arrives after the reset
  assert.equal(r1.value(), 10);
  assert.equal(r2.value(), 10);
  assert.ok(r1.equals(r2));
});

test("overlapping concurrent resets clear the union of their observed sets", () => {
  const r1 = new ObservedResetCounter("r1");
  const r2 = new ObservedResetCounter("r2");
  const i1 = r1.increment(1);
  const i2 = r2.increment(2);
  const i3 = r1.increment(4);
  r2.apply(i1); // r2 observes {i2, i1}; r1 never sees i2 before resetting
  const reset1 = r1.reset(); // observes {i1, i3}
  const reset2 = r2.reset(); // observes {i2, i1} -- overlaps reset1 on i1
  const i4 = r2.increment(8); // concurrent with reset1
  // Exchange everything.
  for (const op of [i2, reset2, i4]) r1.apply(op);
  for (const op of [i3, reset1, i4]) r2.apply(op);
  // Cleared = {i1, i3} union {i1, i2} = {i1, i2, i3}; only i4 survives.
  assert.equal(r1.value(), 8);
  assert.equal(r2.value(), 8);
  assert.ok(r1.equals(r2));
});

test("disjoint concurrent resets each keep the other's unobserved increments", () => {
  const r1 = new ObservedResetCounter("r1");
  const r2 = new ObservedResetCounter("r2");
  const a = r1.increment(5); // only r1 knows
  const b = r2.increment(6); // only r2 knows
  const reset1 = r1.reset(); // observes {a}
  const reset2 = r2.reset(); // observes {b}
  // Full gossip.
  for (const op of [b, reset2]) r1.apply(op);
  for (const op of [a, reset1]) r2.apply(op);
  // reset1 clears a, reset2 clears b; nothing else to clear -> value 0.
  assert.equal(r1.value(), 0);
  assert.equal(r2.value(), 0);
  // A later increment on top of the merged state counts again.
  r1.increment(2);
  r2.merge(r1.snapshot());
  assert.equal(r2.value(), 2);
});

test("duplicate delivery and repeated merge are idempotent", () => {
  const r1 = new ObservedResetCounter("r1");
  const r2 = new ObservedResetCounter("r2");
  const a = r1.increment(3);
  const reset = r1.reset();
  const b = r1.increment(1);
  assert.equal(r2.apply(a), true);
  assert.equal(r2.apply(a), false); // duplicate: ignored
  r2.apply(reset);
  r2.apply(b);
  const before = r2.snapshot();
  r2.merge(r1.snapshot()).merge(r1.snapshot()); // re-merge same state
  r2.apply(b); // duplicate again
  assert.equal(r2.value(), 1);
  assert.deepEqual(r2.snapshot(), before);
  assert.equal(r2.size, 3);
});

test("merge is commutative and associative", () => {
  const make = (): [ObservedResetCounter, ObservedResetCounter, ObservedResetCounter] => {
    const r1 = new ObservedResetCounter("r1");
    const r2 = new ObservedResetCounter("r2");
    const r3 = new ObservedResetCounter("r3");
    const a = r1.increment(1);
    const b = r2.increment(2);
    r1.apply(b);
    const reset = r1.reset(); // observes {a, b}
    const c = r3.increment(4);
    return [
      new ObservedResetCounter("x", { ops: [a, b, reset] }),
      new ObservedResetCounter("y", { ops: [c, reset] }),
      new ObservedResetCounter("z", { ops: [a, c] }),
    ];
  };
  const [x1, y1, z1] = make();
  const [x2, y2, z2] = make();
  const left = x1.merge(y1.snapshot(), z1.snapshot());
  const right = z2.merge(y2.snapshot()).merge(x2.snapshot());
  assert.ok(left.equals(right));
  assert.equal(left.value(), 4);
});

test("message permutation invariance: every delivery order converges", () => {
  // Build a fixed scenario across three replicas with resets in the mix.
  const r1 = new ObservedResetCounter("r1");
  const r2 = new ObservedResetCounter("r2");
  const r3 = new ObservedResetCounter("r3");
  const a = r1.increment(1);
  const b = r2.increment(2);
  r1.apply(b);
  const reset1 = r1.reset(); // observes {a, b}
  const c = r2.increment(3); // concurrent with reset1
  const d = r3.increment(4);
  r3.apply(a);
  const reset2 = r3.reset(); // observes {d, a}
  const e = r1.increment(5); // after reset1
  const messages: CounterOp[] = [a, b, reset1, c, d, reset2, e];
  const expected = modelValue(messages);
  assert.equal(expected, 3 + 5); // reset1 clears {a,b}; reset2 clears {d,a}

  let reference: CounterSnapshot | undefined;
  let checked = 0;
  for (const perm of permutations(messages)) {
    const replica = new ObservedResetCounter("p");
    for (const op of perm) replica.apply(op);
    assert.equal(replica.value(), expected);
    if (!reference) reference = replica.snapshot();
    else assert.deepEqual(replica.snapshot(), reference);
    checked++;
  }
  assert.equal(checked, 5040); // 7! permutations
});

test("randomized convergence: gossip in any order reaches the model value", () => {
  const rng = mulberry32(0x5eed);
  const replicaIds = ["r1", "r2", "r3", "r4", "r5"];
  const replicas = replicaIds.map((id) => new ObservedResetCounter(id));
  const allOps: CounterOp[] = [];

  for (let step = 0; step < 300; step++) {
    const roll = rng();
    if (roll < 0.6) {
      const r = replicas[Math.floor(rng() * replicas.length)];
      allOps.push(rng() < 0.75 ? r.increment(1 + Math.floor(rng() * 5)) : r.reset());
    } else {
      const a = replicas[Math.floor(rng() * replicas.length)];
      const b = replicas[Math.floor(rng() * replicas.length)];
      if (a !== b) {
        a.merge(b.snapshot());
        b.merge(a.snapshot());
      }
    }
  }

  // Final full gossip from randomly shuffled snapshots.
  const snapshots = shuffled(replicas.map((r) => r.snapshot()), rng);
  const merged = replicas.map(() => {
    const r = new ObservedResetCounter("sink");
    for (const snap of shuffled(snapshots, rng)) r.merge(snap);
    return r;
  });

  const expected = modelValue(allOps);
  for (const r of merged) {
    assert.equal(r.value(), expected);
    assert.ok(r.equals(merged[0]));
  }
  // And every original replica converges after one more gossip round.
  for (const r of replicas) r.merge(merged[0].snapshot());
  for (const r of replicas) assert.equal(r.value(), expected);
});

test("reset observing another reset is harmless and never resurrects cleared ops", () => {
  const r1 = new ObservedResetCounter("r1");
  const a = r1.increment(9);
  const reset1 = r1.reset(); // observes {a}
  const reset2 = r1.reset(); // observes {a, reset1}
  assert.equal(r1.value(), 0);
  const r2 = new ObservedResetCounter("r2");
  // Deliver out of order: reset2 first, then the ops it references.
  r2.apply(reset2);
  r2.apply(reset1);
  r2.apply(a);
  assert.equal(r2.value(), 0);
  assert.ok(r1.equals(r2));
});

test("snapshots are JSON-round-trippable and deep-copied", () => {
  const r1 = new ObservedResetCounter("r1");
  const a = r1.increment(2);
  const reset = r1.reset();
  const wire = JSON.parse(JSON.stringify(r1.snapshot())) as CounterSnapshot;
  const r2 = new ObservedResetCounter("r2", wire);
  assert.equal(r2.value(), 0);
  assert.ok(r1.equals(r2));
  // Mutating the snapshot must not affect the source replica.
  const snap = r1.snapshot();
  (snap.ops[0] as IncOp).amount = 999;
  ((snap.ops[1] as ResetOp).observed as string[]).length = 0;
  assert.equal(r1.value(), 0);
  assert.ok(r1.has(reset.id));
  assert.ok(r1.has(a.id));
});

test("invalid operations and amounts are rejected", () => {
  const r = new ObservedResetCounter("r1");
  assert.throws(() => r.increment(0), RangeError);
  assert.throws(() => r.increment(-2), RangeError);
  assert.throws(() => r.increment(1.5), RangeError);
  assert.throws(
    () => r.apply({ kind: "inc", id: "", amount: 1 } as IncOp),
    TypeError,
  );
  assert.throws(
    () =>
      r.apply({ kind: "reset", id: "x#1", observed: "not-an-array" } as unknown as ResetOp),
    TypeError,
  );
});
