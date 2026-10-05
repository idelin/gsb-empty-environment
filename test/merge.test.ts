import { test } from "node:test";
import assert from "node:assert/strict";
import { Replica, type Message } from "../src/replica.ts";
import { gossip, sync, syncAll } from "../src/sync.ts";
import {
  compare,
  dominates,
  clockKey,
  type VectorClock,
} from "../src/vector-clock.ts";

type Snap = [string, [string, unknown][]][];

/** Deterministic snapshot: sorted keys, versions sorted by clock. */
function snapshot(replica: Replica<unknown>): Snap {
  return replica.keys().map((key) => [
    key,
    replica
      .get(key)
      .map((version) => [clockKey(version.clock), version.value] as [string, unknown])
      .sort((a, b) => (a[0] < b[0] ? -1 : 1)),
  ]);
}

function assertConverged(replicas: Replica<unknown>[]): void {
  const first = snapshot(replicas[0]);
  for (const replica of replicas.slice(1)) {
    assert.deepEqual(snapshot(replica), first);
  }
}

/** Deterministic PRNG (mulberry32) for shuffles. */
function rng(seed: number): () => number {
  let state = seed;
  return () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function shuffled<T>(items: T[], seed: number): T[] {
  const out = [...items];
  const rand = rng(seed);
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

test("vector clock ordering: equal / before / after / concurrent", () => {
  const a: VectorClock = { A: 1 };
  const b: VectorClock = { A: 2 };
  const c: VectorClock = { A: 1, B: 1 };
  assert.equal(compare(a, { A: 1 }), "equal");
  assert.equal(compare(a, b), "before");
  assert.equal(compare(b, a), "after");
  assert.equal(compare(b, c), "concurrent");
  assert.ok(dominates(b, a));
  assert.ok(!dominates(a, b));
  assert.ok(!dominates(b, c));
  assert.ok(!dominates(a, a));
});

test("dominance: sequential writes on one replica keep only the latest", () => {
  const a = new Replica<string>("A");
  a.put("k", "v1");
  a.put("k", "v2");
  const v3 = a.put("k", "v3");
  assert.deepEqual(a.values("k"), ["v3"]);
  assert.ok(dominates(v3.clock, { A: 2 }));
});

test("concurrent offline writes on three replicas are all retained", () => {
  const a = new Replica<string>("A");
  const b = new Replica<string>("B");
  const c = new Replica<string>("C");
  a.put("k", "from-A");
  b.put("k", "from-B");
  c.put("k", "from-C");

  syncAll([a, b, c]);

  for (const replica of [a, b, c]) {
    assert.deepEqual(
      replica.values("k").sort(),
      ["from-A", "from-B", "from-C"],
    );
  }
  assertConverged([a, b, c]);
});

test("explicit resolve covers every seen version and converges to one value", () => {
  const a = new Replica<string>("A");
  const b = new Replica<string>("B");
  const c = new Replica<string>("C");
  const va = a.put("k", "from-A");
  const vb = b.put("k", "from-B");
  const vc = c.put("k", "from-C");
  syncAll([a, b, c]);

  const resolved = a.resolve("k", "resolved");
  for (const seen of [va, vb, vc]) {
    assert.ok(dominates(resolved.clock, seen.clock));
  }

  syncAll([a, b, c]);
  for (const replica of [a, b, c]) {
    assert.deepEqual(replica.values("k"), ["resolved"]);
  }
  assertConverged([a, b, c]);
});

test("resolve never deletes versions the caller has not seen", () => {
  const a = new Replica<string>("A");
  const b = new Replica<string>("B");
  const c = new Replica<string>("C");

  const va = a.put("k", "from-A");
  const vb = b.put("k", "from-B");
  sync(a, b); // A and B know each other; C is still offline.

  const vc = c.put("k", "from-C"); // unseen by A and B
  const resolved = a.resolve("k", "resolved-by-A");

  // The resolution covers exactly what A saw, and stays concurrent with C's.
  assert.ok(dominates(resolved.clock, va.clock));
  assert.ok(dominates(resolved.clock, vb.clock));
  assert.equal(compare(resolved.clock, vc.clock), "concurrent");

  syncAll([a, b, c]);
  for (const replica of [a, b, c]) {
    assert.deepEqual(replica.values("k").sort(), ["from-C", "resolved-by-A"]);
  }
  assertConverged([a, b, c]);
});

test("resolve with an explicit seen-list covers exactly those versions", () => {
  const a = new Replica<string>("A");
  const b = new Replica<string>("B");
  const va = a.put("k", "from-A");
  const vb = b.put("k", "from-B"); // never propagated to A

  // Caller passes the clocks it saw (e.g. read earlier, since pruned locally).
  const resolved = a.resolve("k", "resolved", [va.clock, vb.clock]);
  assert.ok(dominates(resolved.clock, va.clock));
  assert.ok(dominates(resolved.clock, vb.clock));

  syncAll([a, b]);
  assert.deepEqual(a.values("k"), ["resolved"]);
  assert.deepEqual(b.values("k"), ["resolved"]);
});

/** Build a cluster with concurrent writes and a resolution, fully synced. */
function buildCluster(): {
  replicas: Replica<string>[];
  expected: Snap;
} {
  const a = new Replica<string>("A");
  const b = new Replica<string>("B");
  const c = new Replica<string>("C");
  a.put("x", "a1");
  b.put("x", "b1");
  c.put("y", "c1");
  a.put("y", "a1");
  syncAll([a, b, c]);
  b.resolve("x", "x-resolved");
  c.put("y", "c2");
  syncAll([a, b, c]);
  return { replicas: [a, b, c], expected: snapshot(a) };
}

test("exchange: pairwise sync order does not change the final set", () => {
  const makeCluster = (): Replica<string>[] => {
    const a = new Replica<string>("A");
    const b = new Replica<string>("B");
    const c = new Replica<string>("C");
    a.put("k", "from-A");
    b.put("k", "from-B");
    c.put("k", "from-C");
    return [a, b, c];
  };

  const forward = makeCluster();
  sync(forward[0], forward[1]);
  sync(forward[1], forward[2]);
  sync(forward[0], forward[2]);

  const backward = makeCluster();
  sync(backward[2], backward[1]);
  sync(backward[1], backward[0]);
  sync(backward[2], backward[0]);

  const mixed = makeCluster();
  gossip(mixed[2], mixed[0]);
  gossip(mixed[1], mixed[2]);
  gossip(mixed[0], mixed[1]);
  gossip(mixed[2], mixed[1]);
  gossip(mixed[1], mixed[0]);
  gossip(mixed[0], mixed[2]);

  assertConverged([...forward, ...backward, ...mixed]);
});

test("duplicate delivery yields the same set as single delivery", () => {
  const { replicas, expected } = buildCluster();
  const messages = replicas.flatMap((replica) => replica.emit());

  const once = new Replica<string>("R");
  for (const message of messages) once.receive(message);

  const twice = new Replica<string>("R");
  for (const message of [...messages, ...messages]) twice.receive(message);

  const thriceShuffled = new Replica<string>("R");
  const tripled = [...messages, ...messages, ...messages];
  for (const message of shuffled(tripled, 42)) thriceShuffled.receive(message);

  assert.deepEqual(snapshot(once), expected);
  assert.deepEqual(snapshot(twice), expected);
  assert.deepEqual(snapshot(thriceShuffled), expected);
});

test("out-of-order delivery converges to the same set for every permutation", () => {
  const { replicas, expected } = buildCluster();
  const messages = replicas.flatMap((replica) => replica.emit());
  assert.ok(messages.length >= 6, "scenario should produce enough messages");

  for (let seed = 1; seed <= 25; seed++) {
    const fresh = new Replica<string>("R");
    for (const message of shuffled(messages, seed)) fresh.receive(message);
    assert.deepEqual(snapshot(fresh), expected, `seed ${seed}`);
  }
});

test("out-of-order + duplicates across replicas converge cluster-wide", () => {
  const a = new Replica<string>("A");
  const b = new Replica<string>("B");
  const c = new Replica<string>("C");
  a.put("k", "a1");
  b.put("k", "b1");
  c.put("k", "c1");
  syncAll([a, b, c]);
  a.resolve("k", "resolved");
  c.put("j", "c-only");

  // Converge the originals to learn the expected final set, then redeliver
  // every in-flight message (shuffled + duplicated) to fresh replicas.
  const streams = [a, b, c].map((replica) => replica.emit());
  syncAll([a, b, c]);
  const expected = snapshot(a);

  const targets = [new Replica<string>("A"), new Replica<string>("B"), new Replica<string>("C")];
  for (const [index, target] of targets.entries()) {
    const inbound = streams.flat();
    const duplicated = inbound.flatMap((message: Message<string>) => [
      message,
      message,
    ]);
    for (const message of shuffled(duplicated, 1000 + index)) {
      target.receive(message);
    }
    assert.deepEqual(snapshot(target), expected);
  }

  assertConverged([a, b, c, ...targets]);
});
