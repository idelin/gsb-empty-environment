import { test } from "node:test";
import assert from "node:assert/strict";
import { extractPathsFromRaw } from "../src/selective.ts";
import { flattenJson } from "../src/flatten.ts";
import { buildOrder, buildOrderRaw } from "../examples/sample-orders.ts";

test("extracts user.address.city without deserializing items", () => {
  const raw = buildOrderRaw(7, "Hangzhou");
  const { values, stats } = extractPathsFromRaw(raw, ["user.address.city"]);

  assert.equal(values.get("user.address.city"), "Hangzhou");
  assert.deepEqual(stats.missingPaths, []);

  // Only the city string is materialized; the items list, payment and
  // logistics subtrees are skipped by a raw scan and never parsed.
  assert.equal(stats.materializedValues, 1);
  assert.ok(stats.skippedValues > 0);

  // A full JSON.parse + flatten would have built every leaf node.
  const fullLeafCount = flattenJson(
    JSON.parse(raw) as Parameters<typeof flattenJson>[0],
  ).size;
  assert.ok(fullLeafCount > 20);
  assert.ok(stats.materializedValues < fullLeafCount);
});

test("extracted values match JSON.parse ground truth", () => {
  const raw = buildOrderRaw(3, "Shanghai");
  const parsed = JSON.parse(raw);
  const paths = [
    "user.address.city",
    "items[1].price",
    "logistics.routes[0].hub",
    "payment.coupon.code",
  ];
  const { values } = extractPathsFromRaw(raw, paths);
  assert.equal(values.get("user.address.city"), parsed.user.address.city);
  assert.equal(values.get("items[1].price"), parsed.items[1].price);
  assert.equal(
    values.get("logistics.routes[0].hub"),
    parsed.logistics.routes[0].hub,
  );
  assert.equal(values.get("payment.coupon.code"), parsed.payment.coupon.code);
});

test("skips array siblings that are not requested", () => {
  const raw = buildOrderRaw(5, "Ningbo");
  const { values, stats } = extractPathsFromRaw(raw, ["items[1].price"]);
  assert.equal(values.get("items[1].price"), 39.9);
  // items[0] and items[2] subtrees are skipped, only the price is built.
  assert.equal(stats.materializedValues, 1);
});

test("skipped regions may contain escapes and unicode", () => {
  const raw = JSON.stringify({
    junk: { note: "quote:\" backslash:\\ newline:\n unicode:✓" },
    target: { leaf: 42 },
  });
  const { values, stats } = extractPathsFromRaw(raw, ["target.leaf"]);
  assert.equal(values.get("target.leaf"), 42);
  assert.equal(stats.materializedValues, 1);
});

test("wanted path pointing at a subtree materializes that subtree", () => {
  const raw = buildOrderRaw(2, "Suzhou");
  const parsed = JSON.parse(raw);
  const { values } = extractPathsFromRaw(raw, ["user.address.geo"]);
  assert.deepEqual(values.get("user.address.geo"), parsed.user.address.geo);
});

test("missing wanted paths are reported, not thrown", () => {
  const raw = buildOrderRaw(4, "Wenzhou");
  const { values, stats } = extractPathsFromRaw(raw, [
    "user.address.city",
    "user.address.zipcode",
  ]);
  assert.equal(values.get("user.address.city"), "Wenzhou");
  assert.deepEqual(stats.missingPaths, ["user.address.zipcode"]);
});

test("null, boolean and exponent numbers round-trip", () => {
  const raw = '{"a":{"b":null,"c":true,"d":-1.5e3},"e":false}';
  const { values } = extractPathsFromRaw(raw, ["a.b", "a.c", "a.d", "e"]);
  assert.equal(values.get("a.b"), null);
  assert.equal(values.get("a.c"), true);
  assert.equal(values.get("a.d"), -1500);
  assert.equal(values.get("e"), false);
});

test("malformed JSON raises a syntax error", () => {
  assert.throws(
    () => extractPathsFromRaw('{"a": }', ["a"]),
    SyntaxError,
  );
});

test("ground-truth order sample stays in sync with expectations", () => {
  const order = buildOrder(9, "Hangzhou") as Record<string, unknown>;
  const user = order.user as Record<string, Record<string, unknown>>;
  assert.equal(user.address.city, "Hangzhou");
});
