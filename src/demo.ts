import { DependencyGraph } from "./graph.ts";
import { InMemoryTargetDb } from "./target-db.ts";
import { CdcReplayScheduler } from "./scheduler.ts";
import type { ChangeEvent } from "./types.ts";

/**
 * Demo: Order depends on User, OrderItem depends on Order.
 * The CDC batch arrives out of order (OrderItem insert BEFORE User insert).
 * The scheduler suspends the OrderItem insert until User and Order commit.
 */

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

const graph = buildGraph();
console.log("== FK dependency graph ==");
console.log("insert order (parents first):", graph.topoOrder().join(" -> "));
console.log("delete order (children first):", graph.deleteOrder().join(" -> "));

// ---- Scenario 1: out-of-order INSERT batch --------------------------------
console.log("\n== Scenario 1: out-of-order INSERT batch ==");
console.log("arrival: OrderItem#1001, Order#101, User#1 (worst-case order)\n");

const db = new InMemoryTargetDb(graph);
const scheduler = new CdcReplayScheduler(db, graph, {
  concurrency: 3,
  onTrace: (line) => console.log("  " + line),
});

const inserts: ChangeEvent[] = [
  { id: "e1", seq: 1, table: "OrderItem", op: "insert", row: { id: 1001, orderId: 101, sku: "book" } },
  { id: "e2", seq: 2, table: "Order", op: "insert", row: { id: 101, userId: 1 } },
  { id: "e3", seq: 3, table: "User", op: "insert", row: { id: 1, name: "alice" } },
];

const r1 = await scheduler.replay(inserts);
console.log("\ncommit order:", r1.appliedOrder.join(", "));
console.log("target db:", JSON.stringify(db.snapshot()));

// ---- Scenario 2: out-of-order DELETE batch --------------------------------
console.log("\n== Scenario 2: out-of-order DELETE batch ==");
console.log("arrival: User#1 delete first, OrderItem delete last\n");

const deletes: ChangeEvent[] = [
  { id: "e4", seq: 4, table: "User", op: "delete", row: { id: 1 } },
  { id: "e5", seq: 5, table: "Order", op: "delete", row: { id: 101 } },
  { id: "e6", seq: 6, table: "OrderItem", op: "delete", row: { id: 1001 } },
];

const r2 = await scheduler.replay(deletes);
console.log("\ncommit order:", r2.appliedOrder.join(", "));
console.log("target db:", JSON.stringify(db.snapshot()));
console.log("\nDone — zero foreign-key violations.");
