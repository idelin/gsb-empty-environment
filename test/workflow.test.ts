import { test } from "./harness";
import assert from "node:assert/strict";
import {
  auditUpgrade,
  defineGraph,
  GraphRegistry,
  InMemoryEventStore,
  InvalidTransitionError,
  migrateBatch,
  StaleVersionError,
  UnknownGraphVersionError,
  UnmappedStateError,
  WorkflowEngine,
  WorkflowError,
  type StateMapping,
} from "../src";

const graphV1 = defineGraph({
  name: "order",
  version: "v1",
  initialState: "placed",
  states: ["placed", "paid", "packing", "shipped", "refunded", "closed"],
  transitions: [
    { from: "placed", action: "pay", to: "paid" },
    { from: "paid", action: "pack", to: "packing" },
    { from: "packing", action: "ship", to: "shipped" },
    { from: "paid", action: "refund", to: "refunded" },
    { from: "shipped", action: "close", to: "closed" },
    { from: "refunded", action: "close", to: "closed" },
  ],
  terminalStates: ["closed"],
});

const graphV2 = defineGraph({
  name: "order",
  version: "v2",
  initialState: "created",
  states: ["created", "processing", "fulfilled", "cancelled", "done"],
  transitions: [
    { from: "created", action: "pay", to: "processing" },
    { from: "processing", action: "pack", to: "processing" },
    { from: "processing", action: "ship", to: "fulfilled" },
    { from: "processing", action: "cancel", to: "cancelled" },
    { from: "fulfilled", action: "close", to: "done" },
    { from: "cancelled", action: "close", to: "done" },
  ],
  terminalStates: ["done"],
});

// paid and packing both merge into processing.
const FULL_MAPPING: StateMapping = {
  placed: "created",
  paid: "processing",
  packing: "processing",
  shipped: "fulfilled",
  refunded: "cancelled",
  closed: "done",
};

function makeEngine(): WorkflowEngine {
  const registry = new GraphRegistry().register(graphV1).register(graphV2);
  return new WorkflowEngine(registry, new InMemoryEventStore());
}

test("restores an instance from an old-version event log", () => {
  const engine = makeEngine();
  engine.createInstance("order-1", "v1");
  engine.dispatch("order-1", { action: "pay", expectedVersion: "v1" });
  engine.dispatch("order-1", { action: "pack", expectedVersion: "v1" });
  const archived = engine.store.export("order-1");

  const restored = makeEngine();
  const view = restored.restoreInstance("order-1", archived);
  assert.equal(view.version, "v1");
  assert.equal(view.state, "packing");
  assert.deepEqual(view.availableActions, ["ship"]);
});

test("restore rejects logs referencing unregistered graph versions", () => {
  const engine = makeEngine();
  engine.createInstance("order-1", "v1");
  const archived = engine.store.export("order-1");

  const registry = new GraphRegistry().register(graphV2);
  const other = new WorkflowEngine(registry, new InMemoryEventStore());
  assert.throws(
    () => other.restoreInstance("order-1", archived),
    UnknownGraphVersionError,
  );
});

test("merging several old states keeps the full event history", () => {
  const engine = makeEngine();
  engine.createInstance("order-a", "v1");
  engine.dispatch("order-a", { action: "pay", expectedVersion: "v1" });
  engine.createInstance("order-b", "v1");
  engine.dispatch("order-b", { action: "pay", expectedVersion: "v1" });
  engine.dispatch("order-b", { action: "pack", expectedVersion: "v1" });

  const result = migrateBatch(
    engine,
    ["order-a", "order-b"],
    "v1",
    "v2",
    FULL_MAPPING,
  );
  assert.equal(result.migrated.length, 2);

  for (const id of ["order-a", "order-b"]) {
    const view = engine.inspect(id);
    assert.equal(view.version, "v2");
    assert.equal(view.state, "processing");
  }

  // History is untouched: order-b keeps created + pay + pack + migration.
  const log = engine.store.load("order-b");
  assert.equal(log.length, 4);
  assert.deepEqual(
    log.map((envelope) => envelope.payload.type),
    ["created", "transition", "transition", "migration"],
  );
  const migration = log[3]!;
  assert.equal(migration.payload.type, "migration");
  if (migration.payload.type === "migration") {
    assert.equal(migration.payload.fromState, "packing");
    assert.equal(migration.payload.toState, "processing");
  }
});

test("an unmapped active state aborts the whole batch", () => {
  const engine = makeEngine();
  engine.createInstance("order-ok", "v1");
  engine.dispatch("order-ok", { action: "pay", expectedVersion: "v1" });
  engine.createInstance("order-stuck", "v1");
  engine.dispatch("order-stuck", { action: "pay", expectedVersion: "v1" });
  engine.dispatch("order-stuck", { action: "pack", expectedVersion: "v1" });

  const partialMapping: StateMapping = { ...FULL_MAPPING };
  delete (partialMapping as Record<string, string>).packing;

  assert.throws(
    () => migrateBatch(engine, ["order-ok", "order-stuck"], "v1", "v2", partialMapping),
    (error: unknown) => {
      assert.ok(error instanceof UnmappedStateError);
      assert.deepEqual(error.unmapped, [
        { instanceId: "order-stuck", state: "packing" },
      ]);
      return true;
    },
  );

  // Nothing was written: the mappable instance is still fully on v1.
  const view = engine.inspect("order-ok");
  assert.equal(view.version, "v1");
  assert.equal(view.state, "paid");
  assert.equal(engine.store.load("order-ok").length, 2);
});

test("terminal instances without a mapping do not block the batch", () => {
  const engine = makeEngine();
  engine.createInstance("order-done", "v1");
  engine.dispatch("order-done", { action: "pay", expectedVersion: "v1" });
  engine.dispatch("order-done", { action: "refund", expectedVersion: "v1" });
  engine.dispatch("order-done", { action: "close", expectedVersion: "v1" });
  engine.createInstance("order-live", "v1");
  engine.dispatch("order-live", { action: "pay", expectedVersion: "v1" });

  const activeOnlyMapping: StateMapping = { ...FULL_MAPPING };
  delete (activeOnlyMapping as Record<string, string>).closed;

  const result = migrateBatch(
    engine,
    ["order-done", "order-live"],
    "v1",
    "v2",
    activeOnlyMapping,
  );
  assert.deepEqual(
    result.skipped.map((entry) => entry.instanceId),
    ["order-done"],
  );
  assert.equal(engine.inspect("order-done").version, "v1");
  assert.equal(engine.inspect("order-done").state, "closed");
  assert.equal(engine.inspect("order-live").version, "v2");
});

test("migration and new events share one atomic sequence", () => {
  const engine = makeEngine();
  engine.createInstance("order-1", "v1");
  engine.dispatch("order-1", { action: "pay", expectedVersion: "v1" });
  migrateBatch(engine, ["order-1"], "v1", "v2", FULL_MAPPING);

  const event = engine.dispatch("order-1", {
    action: "ship",
    expectedVersion: "v2",
  });
  const log = engine.store.load("order-1");
  assert.equal(event.seq, log.length);
  assert.deepEqual(
    log.map((envelope) => envelope.seq),
    [1, 2, 3, 4],
  );
  assert.equal(log[2]!.payload.type, "migration");
  assert.equal(log[3]!.payload.type, "transition");
});

test("late commands issued against the old version are rejected", () => {
  const engine = makeEngine();
  engine.createInstance("order-1", "v1");
  engine.dispatch("order-1", { action: "pay", expectedVersion: "v1" });
  migrateBatch(engine, ["order-1"], "v1", "v2", FULL_MAPPING);

  assert.throws(
    () => engine.dispatch("order-1", { action: "pack", expectedVersion: "v1" }),
    (error: unknown) => {
      assert.ok(error instanceof StaleVersionError);
      assert.equal(error.expected, "v1");
      assert.equal(error.actual, "v2");
      return true;
    },
  );
  // The rejected command left no trace in the log.
  assert.equal(engine.store.load("order-1").length, 3);
});

test("commands for actions removed by the new graph are rejected", () => {
  const engine = makeEngine();
  engine.createInstance("order-1", "v1");
  engine.dispatch("order-1", { action: "pay", expectedVersion: "v1" });
  migrateBatch(engine, ["order-1"], "v1", "v2", FULL_MAPPING);
  assert.throws(
    () => engine.dispatch("order-1", { action: "refund", expectedVersion: "v2" }),
    InvalidTransitionError,
  );
});

test("history replay verifies executable actions before and after upgrade", () => {
  const engine = makeEngine();
  engine.createInstance("order-1", "v1");
  engine.dispatch("order-1", { action: "pay", expectedVersion: "v1" });
  migrateBatch(engine, ["order-1"], "v1", "v2", FULL_MAPPING);

  const audit = auditUpgrade(engine, "order-1");
  assert.equal(audit.migrationSeq, 3);
  assert.equal(audit.before.version, "v1");
  assert.equal(audit.before.state, "paid");
  assert.deepEqual(audit.before.availableActions, ["pack", "refund"]);
  assert.equal(audit.after.version, "v2");
  assert.equal(audit.after.state, "processing");
  assert.deepEqual(audit.after.availableActions, ["pack", "ship", "cancel"]);
});

test("audit refuses instances that were never migrated", () => {
  const engine = makeEngine();
  engine.createInstance("order-1", "v1");
  assert.throws(() => auditUpgrade(engine, "order-1"), WorkflowError);
});

test("mapping onto an unknown new-graph state is rejected up front", () => {
  const engine = makeEngine();
  engine.createInstance("order-1", "v1");
  assert.throws(
    () =>
      migrateBatch(engine, ["order-1"], "v1", "v2", {
        ...FULL_MAPPING,
        placed: "nowhere",
      }),
    WorkflowError,
  );
});
