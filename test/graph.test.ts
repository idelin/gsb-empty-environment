import { test } from "node:test";
import assert from "node:assert/strict";
import { DependencyGraph } from "../src/graph.ts";

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

test("topoOrder returns parents before children", () => {
  const order = buildGraph().topoOrder();
  assert.ok(order.indexOf("User") < order.indexOf("Order"));
  assert.ok(order.indexOf("Order") < order.indexOf("OrderItem"));
});

test("deleteOrder returns children before parents", () => {
  const order = buildGraph().deleteOrder();
  assert.ok(order.indexOf("OrderItem") < order.indexOf("Order"));
  assert.ok(order.indexOf("Order") < order.indexOf("User"));
});

test("cyclic foreign keys are rejected", () => {
  const g = new DependencyGraph()
    .addTable({ name: "A", primaryKey: "id" })
    .addTable({ name: "B", primaryKey: "id" })
    .addForeignKey({ name: "fk_a_b", childTable: "A", childColumn: "bId", parentTable: "B", parentColumn: "id" })
    .addForeignKey({ name: "fk_b_a", childTable: "B", childColumn: "aId", parentTable: "A", parentColumn: "id" });
  assert.throws(() => g.topoOrder(), /cycle/);
});

test("unknown tables in FK are rejected", () => {
  const g = new DependencyGraph().addTable({ name: "A", primaryKey: "id" });
  assert.throws(
    () => g.addForeignKey({ name: "fk", childTable: "A", childColumn: "x", parentTable: "Nope", parentColumn: "id" }),
    /unknown table/,
  );
});
