import assert from "node:assert/strict";
import { test } from "node:test";
import {
  BatchMigrationError,
  CorruptHistoryError,
  WorkflowEngine,
} from "../src/index.ts";
import { graphV1, graphV2, graphV2Partial } from "./fixtures.ts";

function engineWithV1V2(): WorkflowEngine {
  const engine = new WorkflowEngine();
  engine.registerGraph(graphV1);
  engine.registerGraph(graphV2);
  return engine;
}

test("restores an instance persisted on an old version and upgrades it", () => {
  const engineV1 = new WorkflowEngine();
  engineV1.registerGraph(graphV1);
  engineV1.start("order-1", 1);
  engineV1.dispatch("order-1", { event: "pay", graphVersion: 1 });
  engineV1.dispatch("order-1", { event: "request_refund", graphVersion: 1 });
  const persisted = engineV1.snapshot("order-1").log;

  const engine = engineWithV1V2();
  const restored = engine.restore("order-1", persisted);
  assert.equal(restored.graphVersion, 1);
  assert.equal(restored.state, "refund_pending");

  const [result] = engine.migrateBatch(["order-1"], 2);
  assert.equal(result?.stateBefore, "refund_pending");
  assert.equal(result?.stateAfter, "refunding");

  const upgraded = engine.snapshot("order-1");
  assert.equal(upgraded.graphVersion, 2);
  assert.equal(upgraded.state, "refunding");
  assert.equal(upgraded.log.length, 4);
  assert.deepEqual(
    upgraded.log.map((event) => event.kind),
    ["created", "transition", "transition", "migrated"],
  );
});

test("merging multiple old states into one preserves historical events", () => {
  const engine = engineWithV1V2();
  engine.start("order-a", 1);
  engine.dispatch("order-a", { event: "pay", graphVersion: 1 });
  engine.dispatch("order-a", { event: "request_refund", graphVersion: 1 });

  engine.start("order-b", 1);
  engine.dispatch("order-b", { event: "pay", graphVersion: 1 });
  engine.dispatch("order-b", { event: "request_refund", graphVersion: 1 });
  engine.dispatch("order-b", { event: "escalate_refund", graphVersion: 1 });

  engine.migrateBatch(["order-a", "order-b"], 2);

  const a = engine.snapshot("order-a");
  const b = engine.snapshot("order-b");
  assert.equal(a.state, "refunding");
  assert.equal(b.state, "refunding");

  const aEvents = a.log.filter((e) => e.kind === "transition").map((e) => e.kind === "transition" ? e.event : "");
  const bEvents = b.log.filter((e) => e.kind === "transition").map((e) => e.kind === "transition" ? e.event : "");
  assert.deepEqual(aEvents, ["pay", "request_refund"]);
  assert.deepEqual(bEvents, ["pay", "request_refund", "escalate_refund"]);

  const auditA = engine.audit("order-a");
  const auditB = engine.audit("order-b");
  assert.deepEqual(auditA.migrations[0]?.mergedFrom.sort(), ["refund_pending", "refund_review"]);
  assert.deepEqual(auditB.migrations[0]?.mergedFrom.sort(), ["refund_pending", "refund_review"]);
  assert.equal(auditB.migrations[0]?.stateBefore, "refund_review");
});

test("an unmapped active state blocks the whole batch", () => {
  const engine = new WorkflowEngine();
  engine.registerGraph(graphV1);
  engine.registerGraph(graphV2Partial);

  engine.start("mapped-1", 1);
  engine.dispatch("mapped-1", { event: "pay", graphVersion: 1 });
  engine.start("mapped-2", 1);
  engine.start("unmapped", 1);
  engine.dispatch("unmapped", { event: "pay", graphVersion: 1 });
  engine.dispatch("unmapped", { event: "ship", graphVersion: 1 });

  const before = ["mapped-1", "mapped-2", "unmapped"].map((id) => engine.snapshot(id).log.length);

  assert.throws(
    () => engine.migrateBatch(["mapped-1", "mapped-2", "unmapped"], 2),
    (error: unknown) => {
      assert.ok(error instanceof BatchMigrationError);
      assert.equal(error.failures.length, 1);
      assert.equal(error.failures[0]?.instanceId, "unmapped");
      assert.match(error.failures[0]?.reason ?? "", /shipped/);
      return true;
    },
  );

  const after = ["mapped-1", "mapped-2", "unmapped"].map((id) => engine.snapshot(id));
  assert.deepEqual(after.map((s) => s.log.length), before);
  assert.ok(after.every((s) => s.graphVersion === 1));
});

test("migration and subsequent events share one atomic sequence", () => {
  const engine = engineWithV1V2();
  engine.start("order-1", 1);
  engine.dispatch("order-1", { event: "pay", graphVersion: 1 });
  engine.dispatch("order-1", { event: "request_refund", graphVersion: 1 });

  const [result] = engine.migrateBatch(["order-1"], 2);
  assert.equal(result?.seq, 4);

  const after = engine.dispatch("order-1", { event: "cancel_refund", graphVersion: 2 });
  const seqs = after.log.map((event) => event.seq);
  assert.deepEqual(seqs, [1, 2, 3, 4, 5]);
  assert.equal(after.log[3]?.kind, "migrated");
  assert.equal(after.log[4]?.kind, "transition");
  assert.equal(after.state, "paid");
});

test("instances on the wrong source version block the batch", () => {
  const engine = engineWithV1V2();
  engine.start("old", 1);
  engine.start("already-new", 1);
  engine.migrateBatch(["already-new"], 2);

  assert.throws(
    () => engine.migrateBatch(["old", "already-new"], 2),
    (error: unknown) => {
      assert.ok(error instanceof BatchMigrationError);
      assert.equal(error.failures[0]?.instanceId, "already-new");
      return true;
    },
  );
  assert.equal(engine.snapshot("old").graphVersion, 1);
});

test("restoring a tampered history is rejected", () => {
  const engineV1 = new WorkflowEngine();
  engineV1.registerGraph(graphV1);
  engineV1.start("order-1", 1);
  engineV1.dispatch("order-1", { event: "pay", graphVersion: 1 });
  const persisted = engineV1.snapshot("order-1").log.map((event) => ({ ...event }));
  const tampered = persisted.map((event) =>
    event.kind === "transition" ? { ...event, from: "shipped" } : event,
  );

  const engine = engineWithV1V2();
  assert.throws(() => engine.restore("order-1", tampered), CorruptHistoryError);
});
