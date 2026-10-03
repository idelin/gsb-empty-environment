import { test } from "node:test";
import assert from "node:assert/strict";
import { flattenJson, inferPaths } from "../src/flatten.ts";
import { buildOrder } from "../examples/sample-orders.ts";

test("flattenJson decomposes nested order into leaf paths", () => {
  const flat = flattenJson(buildOrder(1, "Hangzhou"));
  assert.equal(flat.get("user.address.city"), "Hangzhou");
  assert.equal(flat.get("user.address.geo.lat"), 30.2084);
  assert.equal(flat.get("items[0].sku"), "SKU-PHONE-01");
  assert.equal(flat.get("items[1].attrs.warranty"), null);
  assert.equal(flat.get("items[2].attrs.warranty.onsite"), false);
  assert.equal(flat.get("logistics.routes[1].hub"), "Shanghai");
  assert.equal(flat.get("payment.coupon.discount"), 100);
});

test("inferPaths returns sorted virtual column names", () => {
  const paths = inferPaths(buildOrder(1, "Hangzhou"));
  assert.ok(paths.includes("user.address.city"));
  assert.ok(paths.includes("items[0].attrs.warranty.years"));
  const sorted = [...paths].sort();
  assert.deepEqual(paths, sorted);
});

test("flattenJson keeps empty containers as leaves", () => {
  const flat = flattenJson({ a: {}, b: [], c: 1 });
  assert.deepEqual(flat.get("a"), {});
  assert.deepEqual(flat.get("b"), []);
  assert.equal(flat.get("c"), 1);
});
