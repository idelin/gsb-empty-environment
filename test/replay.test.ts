import assert from "node:assert/strict";
import { test } from "node:test";
import { WorkflowEngine } from "../src/index.ts";
import { graphV1, graphV2 } from "./fixtures.ts";

function upgradedEngine(): { engine: WorkflowEngine; id: string } {
  const engine = new WorkflowEngine();
  engine.registerGraph(graphV1);
  engine.registerGraph(graphV2);
  engine.start("order-1", 1);
  engine.dispatch("order-1", { event: "pay", graphVersion: 1 });
  engine.dispatch("order-1", { event: "request_refund", graphVersion: 1 });
  engine.migrateBatch(["order-1"], 2);
  return { engine, id: "order-1" };
}

test("history replay cross-checks executable actions before and after upgrade", () => {
  const { engine, id } = upgradedEngine();
  const audit = engine.audit(id);

  assert.equal(audit.migrations.length, 1);
  const check = audit.migrations[0];
  assert.equal(check?.fromVersion, 1);
  assert.equal(check?.toVersion, 2);
  assert.equal(check?.stateBefore, "refund_pending");
  assert.equal(check?.stateAfter, "refunding");
  assert.deepEqual(check?.actionsBefore, ["escalate_refund", "refund"]);
  assert.deepEqual(check?.actionsAfter, ["cancel_refund", "refund"]);

  assert.deepEqual(audit.final, { graphVersion: 2, state: "refunding" });
  assert.deepEqual(
    audit.entries.map((entry) => entry.availableActions),
    [["pay"], ["ship", "request_refund"], ["escalate_refund", "refund"], ["cancel_refund", "refund"]],
  );
});

test("audit entries stay consistent with live availableActions", () => {
  const { engine, id } = upgradedEngine();
  const audit = engine.audit(id);
  const last = audit.entries[audit.entries.length - 1];
  assert.deepEqual(last?.availableActions, engine.availableActions(id));
});

test("replay validates every historical transition against its graph version", () => {
  const { engine, id } = upgradedEngine();
  engine.dispatch(id, { event: "refund", graphVersion: 2 });
  const audit = engine.audit(id);
  assert.deepEqual(audit.final, { graphVersion: 2, state: "closed" });
  assert.equal(audit.entries.length, 5);
  assert.deepEqual(audit.entries[4]?.availableActions, []);
});
