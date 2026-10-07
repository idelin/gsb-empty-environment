import assert from "node:assert/strict";
import { test } from "node:test";
import {
  InvalidTransitionError,
  StaleCommandError,
  UnknownInstanceError,
  WorkflowEngine,
} from "../src/index.ts";
import { graphV1, graphV2 } from "./fixtures.ts";

test("dispatch appends transitions and tracks available actions", () => {
  const engine = new WorkflowEngine();
  engine.registerGraph(graphV1);
  engine.start("order-1", 1);

  assert.deepEqual(engine.availableActions("order-1"), ["pay"]);
  const snapshot = engine.dispatch("order-1", { event: "pay", graphVersion: 1 });
  assert.equal(snapshot.state, "paid");
  assert.deepEqual(engine.availableActions("order-1"), ["ship", "request_refund"]);
  assert.equal(snapshot.log.length, 2);
  assert.equal(snapshot.log[1]?.kind, "transition");
});

test("invalid transitions are rejected", () => {
  const engine = new WorkflowEngine();
  engine.registerGraph(graphV1);
  engine.start("order-1", 1);

  assert.throws(
    () => engine.dispatch("order-1", { event: "ship", graphVersion: 1 }),
    InvalidTransitionError,
  );
  assert.equal(engine.snapshot("order-1").state, "created");
});

test("unknown instances are rejected", () => {
  const engine = new WorkflowEngine();
  engine.registerGraph(graphV1);
  assert.throws(
    () => engine.dispatch("ghost", { event: "pay", graphVersion: 1 }),
    UnknownInstanceError,
  );
});

test("late commands stamped with an old graph version are rejected", () => {
  const engine = new WorkflowEngine();
  engine.registerGraph(graphV1);
  engine.registerGraph(graphV2);
  engine.start("order-1", 1);
  engine.dispatch("order-1", { event: "pay", graphVersion: 1 });
  engine.dispatch("order-1", { event: "request_refund", graphVersion: 1 });
  engine.migrateBatch(["order-1"], 2);

  assert.throws(
    () => engine.dispatch("order-1", { event: "refund", graphVersion: 1 }),
    (error: unknown) => {
      assert.ok(error instanceof StaleCommandError);
      assert.equal(error.commandVersion, 1);
      assert.equal(error.currentVersion, 2);
      return true;
    },
  );
  assert.equal(engine.snapshot("order-1").state, "refunding");

  const snapshot = engine.dispatch("order-1", { event: "refund", graphVersion: 2 });
  assert.equal(snapshot.state, "closed");
});

test("commands stamped with a future graph version are rejected", () => {
  const engine = new WorkflowEngine();
  engine.registerGraph(graphV1);
  engine.registerGraph(graphV2);
  engine.start("order-1", 1);

  assert.throws(
    () => engine.dispatch("order-1", { event: "pay", graphVersion: 2 }),
    StaleCommandError,
  );
  assert.equal(engine.snapshot("order-1").graphVersion, 1);
});
