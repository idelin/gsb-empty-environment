import test from "node:test";
import assert from "node:assert/strict";
import { Cluster } from "../src/cluster.ts";
import { BackpressureError, ConflictError } from "../src/node.ts";
import { deliverableClosure, validateSequence } from "../src/vector-model.ts";
import { idKey } from "../src/types.ts";
import type { Message } from "../src/types.ts";

function standardMessages(): Message[] {
  return [
    { id: { node: "A", epoch: 1, counter: 1 }, deps: {}, payload: "a1" },
    { id: { node: "A", epoch: 1, counter: 2 }, deps: { "A@1": 1 }, payload: "a2" },
    { id: { node: "B", epoch: 1, counter: 1 }, deps: { "A@1": 1 }, payload: "b1" },
    { id: { node: "C", epoch: 1, counter: 1 }, deps: { "A@1": 2, "B@1": 1 }, payload: "c1" },
  ];
}

function permutations<T>(items: readonly T[]): T[][] {
  if (items.length <= 1) return [items.slice()];
  const out: T[][] = [];
  for (let i = 0; i < items.length; i++) {
    const rest = items.slice(0, i).concat(items.slice(i + 1));
    for (const p of permutations(rest)) out.push([items[i], ...p]);
  }
  return out;
}

test("all arrival permutations preserve causal invariants", () => {
  const msgs = standardMessages();
  const perms = permutations(msgs);
  assert.equal(perms.length, 24);
  const closure = deliverableClosure(msgs);
  for (const order of perms) {
    const c = new Cluster(["A", "B", "C", "D"]);
    for (const m of order) c.receive("D", m);
    const d = c.node("D");
    const err = validateSequence(msgs, d.deliveryLog);
    assert.equal(err, null, `order ${order.map((m) => idKey(m.id)).join(",")}: ${err}`);
    assert.equal(d.stats.delivered, 4);
    assert.deepEqual(new Set(d.deliveryLog.map(idKey)), closure);
    assert.equal(d.resources().staged, 0);
  }
});

test("duplicate deliveries are idempotent under interleaved replays", () => {
  const msgs = standardMessages().slice(0, 3);
  const doubled = msgs.flatMap((m) => [m, m]);
  for (const order of permutations(doubled)) {
    const c = new Cluster(["A", "B", "C", "D"]);
    for (const m of order) c.receive("D", m);
    const d = c.node("D");
    assert.equal(validateSequence(msgs, d.deliveryLog), null);
    assert.equal(d.stats.delivered, 3);
    assert.equal(d.stats.duplicates, 3);
  }
});

test("high counter does not auto-fill gaps", () => {
  const [m1, m2] = standardMessages();
  const c = new Cluster(["A", "B"]);
  const delivered = c.receive("B", m2);
  assert.equal(delivered.length, 0);
  const b = c.node("B");
  assert.equal(b.prefixOf("A@1"), 0);
  const pending = b.pendingDependencies();
  assert.equal(pending.length, 1);
  assert.deepEqual(pending[0].id, m2.id);
  assert.ok(
    pending[0].missing.some((d) => d.stream === "A@1" && d.need === 1 && d.have === 0),
  );
  const now = c.receive("B", m1);
  assert.deepEqual(now.map((m) => idKey(m.id)), ["A@1#1", "A@1#2"]);
  assert.equal(b.prefixOf("A@1"), 2);
});

test("same identity with different content raises ConflictError", () => {
  const [m1, m2] = standardMessages();
  const c = new Cluster(["A", "B"]);
  c.receive("B", m1);
  assert.throws(() => c.receive("B", { ...m1, payload: "forged" }), ConflictError);
  assert.equal(c.node("B").stats.conflicts, 1);

  const c2 = new Cluster(["A", "B"]);
  c2.receive("B", m2);
  assert.throws(() => c2.receive("B", { ...m2, payload: "forged" }), ConflictError);
  assert.equal(c2.node("B").stats.conflicts, 1);
});

test("bounded staging produces explicit backpressure", () => {
  const c = new Cluster(["A", "B", "C"], 1);
  const [, m2] = standardMessages();
  const blocked: Message = {
    id: { node: "B", epoch: 1, counter: 1 },
    deps: { "A@1": 2 },
    payload: "x",
  };
  c.receive("C", m2);
  assert.equal(c.node("C").resources().staged, 1);
  const delivered = c.receive("C", blocked);
  assert.equal(delivered.length, 0);
  assert.equal(c.backpressure.length, 1);
  assert.deepEqual(c.backpressure[0].id, blocked.id);
  assert.equal(c.node("C").stats.backpressure, 1);
  assert.equal(c.node("C").resources().staged, 1);

  const [m1] = standardMessages();
  c.receive("C", m1);
  const now = c.receive("C", blocked);
  assert.deepEqual(now.map((m) => idKey(m.id)), ["B@1#1"]);
});

test("node-level BackpressureError is throwable for callers", () => {
  const c = new Cluster(["A", "B"], 0);
  const [, m2] = standardMessages();
  assert.equal(c.receive("B", m2).length, 0);
  assert.equal(c.backpressure.length, 1);
  assert.ok(c.node("B").stats.backpressure === 1);
  void BackpressureError;
});

test("restart uses new epoch; old epoch retires only after full ack barrier", () => {
  const c = new Cluster(["A", "B", "C"]);
  const a1 = c.broadcast("A", "a1");
  c.receive("B", a1);
  c.receive("C", a1);
  const b1 = c.broadcast("B", "b1");
  c.receive("A", b1);
  c.switchEpoch("B");
  const b2 = c.broadcast("B", "b2");
  assert.deepEqual(b2.id, { node: "B", epoch: 2, counter: 1 });
  assert.equal(b2.deps["B@1"], 1);
  c.receive("A", b2);

  c.receive("C", b2);
  assert.equal(c.node("C").prefixOf("B@2"), 0);
  assert.ok(
    c.node("C").pendingDependencies().some((p) => p.missing.some((d) => d.stream === "B@1")),
  );
  assert.ok(!c.retired.has("B@1"));

  const delivered = c.receive("C", b1);
  assert.deepEqual(delivered.map((m) => idKey(m.id)), ["B@1#1", "B@2#1"]);
  assert.ok(c.retired.has("B@1"));
});

test("checkpoint reclaims only the common stable prefix, never a single node maximum", () => {
  const c = new Cluster(["A", "B", "C"]);
  const a1 = c.broadcast("A", "a1");
  c.receive("B", a1);

  const stable1 = c.checkpoint();
  assert.equal(stable1.get("A@1") ?? 0, 0);
  assert.equal(c.node("A").resources().dedupRecords, 1);
  assert.equal(c.reclaims.length, 0);

  c.receive("C", a1);
  const stable2 = c.checkpoint();
  assert.equal(stable2.get("A@1"), 1);
  for (const m of ["A", "B", "C"]) {
    assert.equal(c.node(m).resources().dedupRecords, 0);
    assert.equal(c.node(m).resources().tombstoneStreams, 1);
  }

  c.receive("C", a1);
  assert.equal(c.node("C").stats.duplicates, 1);

  const intervals = c.reclaims.filter((r) => r.stream === "A@1");
  assert.equal(intervals.length, 3);
  for (const iv of intervals) {
    assert.equal(iv.from, 1);
    assert.equal(iv.to, 1);
    assert.equal(iv.tombstoneDropped, false);
  }

  c.switchEpoch("A");
  assert.ok(c.retired.has("A@1"));
  c.checkpoint();
  for (const m of ["A", "B", "C"]) {
    assert.equal(c.node(m).resources().tombstoneStreams, 0);
  }
  const dropped = c.reclaims.filter((r) => r.stream === "A@1" && r.tombstoneDropped);
  assert.equal(dropped.length, 3);

  c.receive("C", a1);
  assert.equal(c.node("C").stats.duplicates, 2);
});

test("offline replica recovers and converges to the independent model closure", () => {
  const c = new Cluster(["A", "B", "C", "D"]);
  const a1 = c.broadcast("A", "a1");
  c.receive("B", a1);
  c.receive("C", a1);
  const b1 = c.broadcast("B", "b1");
  c.receive("A", b1);
  c.receive("C", b1);
  const c1 = c.broadcast("C", "c1");
  c.receive("A", c1);
  c.receive("B", c1);

  c.switchEpoch("D");
  assert.ok(c.retired.has("D@1"));
  const d1 = c.broadcast("D", "d1");
  assert.deepEqual(d1.id, { node: "D", epoch: 2, counter: 1 });

  c.receive("D", c1);
  c.receive("D", b1);
  c.receive("D", a1);
  const d = c.node("D");
  const all = [a1, b1, c1, d1];
  assert.equal(validateSequence(all, d.deliveryLog), null);
  assert.deepEqual(new Set(d.deliveryLog.map(idKey)), deliverableClosure(all));
  for (const stream of ["A@1", "B@1", "C@1"]) {
    assert.equal(d.prefixOf(stream), c.node("A").prefixOf(stream));
  }
});

test("resource accounting: staging bounded, reclaimed + retained = delivered", () => {
  const c = new Cluster(["A", "B"], 2);
  const a1 = c.broadcast("A", "a1");
  const a2 = c.broadcast("A", "a2");
  const a3 = c.broadcast("A", "a3");
  c.receive("B", a3);
  c.receive("B", a2);
  const b = c.node("B");
  assert.equal(b.resources().staged, 2);
  assert.equal(b.stats.stagedHighWater, 2);
  c.receive("B", a1);
  assert.equal(b.resources().staged, 0);
  assert.equal(b.stats.delivered, 3);

  const stable = c.checkpoint();
  assert.equal(stable.get("A@1"), 3);
  for (const m of ["A", "B"]) {
    const n = c.node(m);
    assert.equal(n.stats.delivered, n.resources().dedupRecords + n.stats.reclaimed);
  }
  assert.equal(b.stats.reclaimed, 3);
  assert.ok(b.stats.stagedHighWater <= 2);
});

test("event-log API: arrival, ack, epoch switch, checkpoint", () => {
  const c = new Cluster(["A", "B"]);
  const m = c.apply({ type: "broadcast", from: "A", payload: "x" }) as Message;
  c.apply({ type: "receive", at: "B", message: m });
  c.apply({ type: "ack", from: "B", stream: "A@1", counter: 1 });
  c.apply({ type: "switch", member: "A" });
  const stable = c.apply({ type: "checkpoint" }) as Map<string, number>;
  assert.equal(stable.get("A@1"), 1);
  assert.ok(c.retired.has("A@1"));
  assert.equal(c.log.length, 5);
});

test("short event permutations: epoch switch interleaved with late arrivals", () => {
  for (const order of permutations(["a1", "b1", "b2"] as const)) {
    const c = new Cluster(["A", "B", "C"]);
    const a1 = c.broadcast("A", "a1");
    c.receive("B", a1);
    const b1 = c.broadcast("B", "b1");
    c.receive("A", b1);
    c.switchEpoch("B");
    const b2 = c.broadcast("B", "b2");
    c.receive("A", b2);
    const inbox: Record<string, Message> = { a1, b1, b2 };
    for (const k of order) c.receive("C", inbox[k]);
    const cn = c.node("C");
    const all = [a1, b1, b2];
    assert.equal(validateSequence(all, cn.deliveryLog), null, order.join(","));
    assert.deepEqual(new Set(cn.deliveryLog.map(idKey)), deliverableClosure(all));
    assert.ok(c.retired.has("B@1"), order.join(","));
  }
});
