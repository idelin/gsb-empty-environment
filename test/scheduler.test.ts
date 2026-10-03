import { test } from "node:test";
import assert from "node:assert/strict";
import { DependencyGraph } from "../src/graph.ts";
import { InMemoryTargetDb } from "../src/target-db.ts";
import { CdcReplayScheduler, DeadlockError } from "../src/scheduler.ts";
import type { ChangeEvent } from "../src/types.ts";

function buildGraph(): DependencyGraph {
  return new DependencyGraph()
    .addTable({ name: "User", primaryKey: "id" })
    .addTable({ name: "Order", primaryKey: "id" })
    .addTable({ name: "OrderItem", primaryKey: "id" })
    .addForeignKey({
      name: "fk_order_user",
      childTable: "Order",
      childColumn: "userId",
      parentTable: "User",
      parentColumn: "id",
    })
    .addForeignKey({
      name: "fk_orderitem_order",
      childTable: "OrderItem",
      childColumn: "orderId",
      parentTable: "Order",
      parentColumn: "id",
    });
}

function makeScheduler(db: InMemoryTargetDb, graph: DependencyGraph, opts = {}) {
  return new CdcReplayScheduler(db, graph, opts);
}

/** Assert every dependent pair in `order` respects FK topology. */
function assertFkOrder(graph: DependencyGraph, events: ChangeEvent[], order: string[]): void {
  const pos = new Map(order.map((id, i) => [id, i]));
  const byId = new Map(events.map((e) => [e.id, e]));
  for (const e of events) {
    if (e.op === "delete") continue;
    for (const fk of graph.foreignKeysOf(e.table)) {
      const parentPk = e.row[fk.childColumn];
      for (const other of events) {
        if (other.op === "delete" || other.table !== fk.parentTable) continue;
        if (other.row[fk.parentColumn] === parentPk) {
          assert.ok(
            pos.get(other.id)! < pos.get(e.id)!,
            `${other.id} (parent) must commit before ${e.id} (child)`,
          );
        }
      }
    }
    void byId;
  }
}

test("out-of-order inserts: OrderItem suspended until User and Order commit", async () => {
  const graph = buildGraph();
  const db = new InMemoryTargetDb(graph);
  const scheduler = makeScheduler(db, graph);

  const events: ChangeEvent[] = [
    { id: "e1", seq: 1, table: "OrderItem", op: "insert", row: { id: 1001, orderId: 101 } },
    { id: "e2", seq: 2, table: "Order", op: "insert", row: { id: 101, userId: 1 } },
    { id: "e3", seq: 3, table: "User", op: "insert", row: { id: 1, name: "alice" } },
  ];

  const result = await scheduler.replay(events);

  // Parent commits strictly before child, despite reverse arrival order.
  assert.deepEqual(result.appliedOrder, ["e3", "e2", "e1"]);
  assert.equal(db.commitLog.join(","), "e3,e2,e1");

  // OrderItem and Order were suspended, then released.
  const suspendedIds = result.suspended.map((s) => s.id);
  assert.ok(suspendedIds.includes("e1"), "OrderItem insert must be suspended");
  assert.ok(suspendedIds.includes("e2"), "Order insert must be suspended");
  assert.ok(result.trace.some((l) => l.startsWith("SUSPEND e1")));
  assert.ok(result.trace.some((l) => l.startsWith("RESUME  e1")));

  // Final state is consistent.
  assert.equal(db.rowCount("User"), 1);
  assert.equal(db.rowCount("Order"), 1);
  assert.equal(db.rowCount("OrderItem"), 1);
});

test("out-of-order deletes: parent delete suspended until children are gone", async () => {
  const graph = buildGraph();
  const db = new InMemoryTargetDb(graph);
  db.insert("User", { id: 1 });
  db.insert("Order", { id: 101, userId: 1 });
  db.insert("OrderItem", { id: 1001, orderId: 101 });
  const scheduler = makeScheduler(db, graph);

  const events: ChangeEvent[] = [
    { id: "d1", seq: 1, table: "User", op: "delete", row: { id: 1 } },
    { id: "d2", seq: 2, table: "Order", op: "delete", row: { id: 101 } },
    { id: "d3", seq: 3, table: "OrderItem", op: "delete", row: { id: 1001 } },
  ];

  const result = await scheduler.replay(events);

  assert.deepEqual(result.appliedOrder, ["d3", "d2", "d1"]);
  assert.ok(result.suspended.map((s) => s.id).includes("d1"));
  assert.equal(db.rowCount("User"), 0);
  assert.equal(db.rowCount("Order"), 0);
  assert.equal(db.rowCount("OrderItem"), 0);
});

test("concurrent replay with random delays never violates FK order", async () => {
  const graph = buildGraph();
  const db = new InMemoryTargetDb(graph);
  const scheduler = makeScheduler(db, graph, {
    concurrency: 4,
    applyDelayMs: () => Math.floor(Math.random() * 5),
  });

  // 50 fully shuffled chains: User_i <- Order_i <- OrderItem_i.
  const events: ChangeEvent[] = [];
  for (let i = 0; i < 50; i++) {
    events.push(
      { id: `u${i}`, seq: 0, table: "User", op: "insert", row: { id: i } },
      { id: `o${i}`, seq: 0, table: "Order", op: "insert", row: { id: 1000 + i, userId: i } },
      { id: `i${i}`, seq: 0, table: "OrderItem", op: "insert", row: { id: 2000 + i, orderId: 1000 + i } },
    );
  }
  // Shuffle arrival order deterministically-ish.
  for (let i = events.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [events[i], events[j]] = [events[j], events[i]];
  }
  events.forEach((e, idx) => (e.seq = idx));

  const result = await scheduler.replay(events);

  assert.equal(result.appliedOrder.length, events.length);
  assertFkOrder(graph, events, result.appliedOrder);
  assert.equal(db.rowCount("User"), 50);
  assert.equal(db.rowCount("Order"), 50);
  assert.equal(db.rowCount("OrderItem"), 50);
});

test("unresolvable dependency raises DeadlockError", async () => {
  const graph = buildGraph();
  const db = new InMemoryTargetDb(graph);
  const scheduler = makeScheduler(db, graph);

  // Order insert whose User parent never arrives.
  const events: ChangeEvent[] = [
    { id: "x1", seq: 1, table: "Order", op: "insert", row: { id: 1, userId: 999 } },
  ];

  await assert.rejects(() => scheduler.replay(events), DeadlockError);
});

test("raw target db rejects FK violations (scheduler must never hit this)", () => {
  const graph = buildGraph();
  const db = new InMemoryTargetDb(graph);
  assert.throws(
    () => db.insert("Order", { id: 1, userId: 42 }),
    /violates FK/,
  );
  db.insert("User", { id: 42 });
  db.insert("Order", { id: 1, userId: 42 });
  assert.throws(() => db.delete("User", 42), /violates FK/);
});
