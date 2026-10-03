import { test } from "node:test";
import assert from "node:assert/strict";
import { ColumnarProjector } from "../src/projector.ts";
import { buildOrder, buildOrderRaw } from "../examples/sample-orders.ts";

test("full ingestion builds one virtual column per leaf path", () => {
  const projector = new ColumnarProjector();
  projector.addRow(buildOrder(1, "Hangzhou"));
  projector.addRow(buildOrder(2, "Shanghai"));

  const names = projector.columnNames();
  assert.ok(names.includes("user.address.city"));
  assert.ok(names.includes("items[0].price"));
  assert.equal(projector.size(), 2);

  const { columns, rowCount } = projector.query(["user.address.city"]);
  assert.equal(rowCount, 2);
  assert.deepEqual(columns["user.address.city"], ["Hangzhou", "Shanghai"]);
});

test("selective raw ingestion only materializes requested paths", () => {
  const projector = new ColumnarProjector();
  const stats = projector.addRowSelective(buildOrderRaw(1, "Hangzhou"), [
    "user.address.city",
  ]);
  assert.equal(stats.materializedValues, 1);
  assert.deepEqual(projector.columnNames(), ["user.address.city"]);

  const { columns } = projector.query(["user.address.city"]);
  assert.deepEqual(columns["user.address.city"], ["Hangzhou"]);
});

test("query returns only the requested columns", () => {
  const projector = new ColumnarProjector();
  projector.addRow(buildOrder(1, "Hangzhou"));
  const result = projector.query(["user.address.city", "payment.method"]);
  assert.deepEqual(Object.keys(result.columns).sort(), [
    "payment.method",
    "user.address.city",
  ]);
  assert.deepEqual(result.columns["payment.method"], ["alipay"]);
});

test("rows missing a path leave undefined slots in that column", () => {
  const projector = new ColumnarProjector();
  projector.addRowSelective(buildOrderRaw(1, "Hangzhou"), [
    "user.address.city",
  ]);
  projector.addRowSelective(buildOrderRaw(2, "Shanghai"), ["payment.method"]);

  const { columns } = projector.query([
    "user.address.city",
    "payment.method",
  ]);
  assert.deepEqual(columns["user.address.city"], ["Hangzhou", undefined]);
  assert.deepEqual(columns["payment.method"], [undefined, "alipay"]);
});

test("inferPaths exposes the virtual column schema of a sample", () => {
  const projector = new ColumnarProjector();
  const paths = projector.inferPaths(buildOrder(1, "Hangzhou"));
  assert.ok(paths.includes("user.address.city"));
  assert.ok(paths.includes("logistics.routes[1].etaHours"));
});
