import assert from "node:assert/strict";
import { test } from "node:test";
import { MessageBus } from "../src/bus.ts";
import { Replica } from "../src/replica.ts";
import type { Operation } from "../src/operations.ts";

function makeCluster(opts?: {
  strategies?: Record<string, string>;
  defaultStrategy?: string;
  clocks?: Record<string, () => number>;
}) {
  const bus = new MessageBus();
  const mk = (id: string) =>
    new Replica({
      id,
      bus,
      strategies: opts?.strategies as never,
      defaultStrategy: opts?.defaultStrategy as never,
      now: opts?.clocks?.[id],
    });
  return { bus, a: mk("A"), b: mk("B"), c: mk("C") };
}

test("concurrent edits to different fields converge and merge", () => {
  const { bus, a, b } = makeCluster();
  a.transact((t) => t.set("title", "hello"));
  b.transact((t) => t.set("count", 3));
  bus.flush();
  assert.deepEqual(a.snapshot(), { title: "hello", count: 3 });
  assert.deepEqual(b.snapshot(), { title: "hello", count: 3 });
});

test("same-field LWW uses (timestamp, replicaId) deterministic tiebreak", () => {
  const { bus, a, b } = makeCluster({
    clocks: { A: () => 5, B: () => 5 },
  });
  a.transact((t) => t.set("color", "red"));
  b.transact((t) => t.set("color", "blue"));
  bus.flush();
  assert.equal(a.get("color"), "blue");
  assert.equal(b.get("color"), "blue");

  const later = makeCluster({ clocks: { A: () => 9, B: () => 5 } });
  later.a.transact((t) => t.set("color", "red"));
  later.b.transact((t) => t.set("color", "blue"));
  later.bus.flush();
  assert.equal(later.a.get("color"), "red");
});

test("multi-value strategy preserves both concurrent writes", () => {
  const { bus, a, b } = makeCluster({
    strategies: { tags: "multi-value" },
    clocks: { A: () => 1, B: () => 1 },
  });
  a.transact((t) => t.set("tags", "x"));
  b.transact((t) => t.set("tags", "y"));
  bus.flush();
  assert.deepEqual(a.get("tags"), ["x", "y"]);
  assert.deepEqual(b.get("tags"), ["x", "y"]);
});

test("a later causal write overrides the field even under multi-value", () => {
  const { bus, a, b } = makeCluster({
    strategies: { tags: "multi-value" },
    clocks: { A: () => 1, B: () => 2 },
  });
  a.transact((t) => t.set("tags", "x"));
  b.transact((t) => t.set("tags", "y"));
  bus.flush();
  b.transact((t) => t.set("tags", "z"));
  bus.flush();
  assert.deepEqual(a.get("tags"), "z");
  assert.deepEqual(b.get("tags"), "z");
});

test("transactions commit atomically: one notification, final state only", () => {
  const { bus, a, b } = makeCluster();
  const seen: Array<Record<string, unknown>> = [];
  let commits = 0;
  b.onCommit((snap) => {
    commits += 1;
    seen.push({ ...snap });
  });
  a.transact((t) => {
    t.set("x", 1);
    t.set("y", 2);
    t.set("z", 3);
  });
  bus.flush();
  assert.equal(commits, 1);
  assert.deepEqual(seen, [{ x: 1, y: 2, z: 3 }]);
});

test("causally out-of-order delivery is buffered until deps arrive", () => {
  const bus = new MessageBus();
  const a = new Replica({ id: "A", bus });
  const b = new Replica({ id: "B", bus });
  a.transact((t) => t.set("first", 1));
  a.transact((t) => t.set("second", 2));
  const ops = [...a.txnLog[0], ...a.txnLog[1]] as Operation[];

  b.receive(ops[1]);
  assert.deepEqual(b.snapshot(), {});
  b.receive(ops[0]);
  assert.deepEqual(b.snapshot(), { first: 1, second: 2 });
});

test("duplicate messages apply once and trigger no extra side effects", () => {
  const bus = new MessageBus();
  const a = new Replica({ id: "A", bus });
  const b = new Replica({ id: "B", bus });
  let commits = 0;
  b.onCommit(() => {
    commits += 1;
  });
  a.transact((t) => t.set("k", "v"));
  const op = a.txnLog[0][0];
  b.receive(op);
  b.receive(op);
  bus.publish(op);
  bus.publish(op);
  bus.flush();
  assert.equal(commits, 1);
  assert.deepEqual(b.snapshot(), { k: "v" });
});

test("local echo after flush does not re-apply or re-fire side effects", () => {
  const bus = new MessageBus();
  const a = new Replica({ id: "A", bus });
  let localCommits = 0;
  let remoteCommits = 0;
  a.onCommit((_snap, txn) => {
    if (txn.local) localCommits += 1;
    else remoteCommits += 1;
  });
  a.transact((t) => t.set("k", "v"));
  bus.flush();
  bus.flush();
  assert.equal(localCommits, 1);
  assert.equal(remoteCommits, 0);
  assert.deepEqual(a.snapshot(), { k: "v" });
});

test("disconnect then reconnect replays missed ops without double-apply", () => {
  const bus = new MessageBus();
  const a = new Replica({ id: "A", bus });
  const b = new Replica({ id: "B", bus });
  let commits = 0;
  b.onCommit(() => {
    commits += 1;
  });

  a.transact((t) => t.set("before", 1));
  bus.flush();
  bus.disconnect("B");
  a.transact((t) => t.set("while-offline", true));
  bus.flush();
  assert.deepEqual(b.snapshot(), { before: 1 });

  bus.reconnect("B");
  bus.flush();
  assert.deepEqual(b.snapshot(), { before: 1, "while-offline": true });
  bus.flush();
  assert.equal(commits, 2);
});

test("state rebuilt by replaying the operation log equals live snapshot", () => {
  const { bus, a, b } = makeCluster({
    strategies: { tags: "multi-value" },
    clocks: { A: () => 1, B: () => 1 },
  });
  a.transact((t) => {
    t.set("title", "hello");
    t.set("tags", "x");
  });
  b.transact((t) => t.set("tags", "y"));
  bus.flush();
  a.transact((t) => t.delete("title"));
  bus.flush();
  assert.deepEqual(b.rebuildFromLog(), b.snapshot());
  assert.deepEqual(a.rebuildFromLog(), a.snapshot());
  assert.deepEqual(a.snapshot(), { tags: ["x", "y"] });
});

test("delete is a causal tombstone under LWW", () => {
  const bus = new MessageBus();
  const a = new Replica({ id: "A", bus, now: () => 1 });
  const b = new Replica({ id: "B", bus, now: () => 2 });
  a.transact((t) => t.set("k", "v"));
  bus.flush();
  a.transact((t) => t.delete("k"));
  bus.flush();
  assert.equal(a.get("k"), undefined);
  assert.equal(b.get("k"), undefined);
});
