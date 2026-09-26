import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ArrayStreamValidator,
  StreamAbortedError,
} from "../src/stream.ts";
import type { StreamSchema } from "../src/schema.ts";

const itemSchema = {
  type: "object",
  properties: {
    id: { type: "integer", minimum: 0 },
    name: { type: "string", minLength: 1 },
  },
  required: ["id", "name"],
  additionalProperties: false,
} as const;

test("valid stream reports pass only after finish()", () => {
  const v = new ArrayStreamValidator({ items: itemSchema, minItems: 2 });
  v.push({ id: 0, name: "a" });
  v.push({ id: 1, name: "b" });
  // Unfinished stream must not report overall success.
  assert.equal(v.verdict, "pending");
  const result = v.finish();
  assert.equal(result.valid, true);
  assert.equal(result.itemCount, 2);
  assert.equal(v.verdict, "valid");
});

test("minItems is only confirmable at finish()", () => {
  const v = new ArrayStreamValidator({ items: itemSchema, minItems: 3 });
  assert.deepEqual(v.push({ id: 0, name: "a" }), []);
  assert.deepEqual(v.push({ id: 1, name: "b" }), []);
  assert.equal(v.verdict, "pending"); // cannot tell yet
  const result = v.finish();
  assert.equal(result.valid, false);
  assert.equal(result.errors.length, 1);
  assert.equal(result.errors[0].instancePath, "");
  assert.equal(result.errors[0].schemaPath, "/minItems");
});

test("maxItems fails early on push()", () => {
  const v = new ArrayStreamValidator({ items: itemSchema, maxItems: 1 });
  assert.deepEqual(v.push({ id: 0, name: "a" }), []);
  const errs = v.push({ id: 1, name: "b" });
  assert.equal(errs.length, 1);
  assert.equal(errs[0].schemaPath, "/maxItems");
  assert.equal(errs[0].instancePath, "/1");
});

test("item errors carry instance path and schema path", () => {
  const v = new ArrayStreamValidator({ items: itemSchema });
  v.push({ id: 0, name: "ok" });
  const errs = v.push({ id: -3, name: 42, extra: true });
  const byKeyword = new Map(errs.map((e) => [e.schemaPath, e.instancePath]));
  assert.equal(byKeyword.get("/items/properties/id/minimum"), "/1/id");
  assert.equal(byKeyword.get("/items/properties/name/type"), "/1/name");
  assert.equal(byKeyword.get("/items/additionalProperties"), "/1/extra");
});

test("missing required property is reported at the item path", () => {
  const v = new ArrayStreamValidator({ items: itemSchema });
  const errs = v.push({ id: 0 });
  assert.equal(errs.length, 1);
  assert.equal(errs[0].instancePath, "/0");
  assert.equal(errs[0].schemaPath, "/items/required");
});

test("100k items: duplicate of the first item at position 99999 is found", () => {
  const v = new ArrayStreamValidator({ items: itemSchema, uniqueItems: true });
  const first = { id: 0, name: "first" };
  v.push(first);
  for (let i = 1; i < 99_999; i++) {
    assert.deepEqual(v.push({ id: i, name: "n" + i }), []);
  }
  // Key order differs on purpose: uniqueness is by value, not text.
  const errs = v.push({ name: "first", id: 0 });
  assert.equal(errs.length, 1);
  assert.equal(errs[0].instancePath, "/99999");
  assert.equal(errs[0].schemaPath, "/uniqueItems");
  assert.equal(v.itemCount, 100_000);
  assert.equal(v.finish().valid, false);
});

test("uniqueKeys detects duplicate key values across the stream", () => {
  const v = new ArrayStreamValidator({ items: itemSchema, uniqueKeys: ["id"] });
  assert.deepEqual(v.push({ id: 7, name: "a" }), []);
  assert.deepEqual(v.push({ id: 8, name: "b" }), []);
  const errs = v.push({ id: 7, name: "c" });
  assert.equal(errs.length, 1);
  assert.equal(errs[0].instancePath, "/2/id");
  assert.equal(errs[0].schemaPath, "/uniqueKeys");
});

test("aggregate max fails early, aggregate min only at finish()", () => {
  const schema: StreamSchema = {
    items: {
      type: "object",
      properties: { amount: { type: "number" } },
      required: ["amount"],
    },
    aggregates: [{ kind: "sum", field: "amount", min: 10, max: 100 }],
  };
  const v = new ArrayStreamValidator(schema);
  assert.deepEqual(v.push({ amount: 60 }), []);
  const errs = v.push({ amount: 50 }); // running sum 110 > 100
  assert.equal(errs.length, 1);
  assert.equal(errs[0].schemaPath, "/aggregates/0/max");
  assert.equal(errs[0].instancePath, "/1/amount");

  const v2 = new ArrayStreamValidator(schema);
  assert.deepEqual(v2.push({ amount: 4 }), []);
  const result = v2.finish(); // sum 4 < min 10
  assert.equal(result.valid, false);
  assert.equal(result.errors[0].schemaPath, "/aggregates/0/min");
  assert.equal(result.errors[0].instancePath, "");
});

test("fail-fast mode aborts on the first error and closes the stream", () => {
  const v = new ArrayStreamValidator({ items: itemSchema }, "fail-fast");
  v.push({ id: 0, name: "a" });
  let aborted: StreamAbortedError | undefined;
  try {
    v.push({ id: "bad", name: "b" });
  } catch (e) {
    aborted = e as StreamAbortedError;
  }
  assert.ok(aborted instanceof StreamAbortedError);
  assert.equal(aborted.errors.length, 1);
  assert.equal(aborted.errors[0].instancePath, "/1/id");
  assert.throws(() => v.push({ id: 1, name: "c" }), /already finished/);
  assert.throws(() => v.finish(), /already called/);
});

test("collect mode gathers errors across many pushes", () => {
  const v = new ArrayStreamValidator({ items: itemSchema }, "collect");
  v.push({ id: "x", name: "a" });
  v.push({ id: 1, name: "" });
  const result = v.finish();
  assert.equal(result.valid, false);
  assert.equal(result.errors.length, 2);
  assert.deepEqual(
    result.errors.map((e) => e.instancePath),
    ["/0/id", "/1/name"],
  );
});

test("finish() may be called only once; push() after finish() throws", () => {
  const v = new ArrayStreamValidator({ items: itemSchema });
  v.push({ id: 0, name: "a" });
  assert.equal(v.finish().valid, true);
  assert.throws(() => v.finish(), /already called/);
  assert.throws(() => v.push({ id: 1, name: "b" }), /already finished/);
});

test("nested arrays and objects validate recursively with full paths", () => {
  const v = new ArrayStreamValidator({
    items: {
      type: "object",
      properties: {
        tags: { type: "array", items: { type: "string" }, minItems: 1 },
      },
      required: ["tags"],
    },
  });
  const errs = v.push({ tags: ["ok", 5] });
  assert.equal(errs.length, 1);
  assert.equal(errs[0].instancePath, "/0/tags/1");
  assert.equal(errs[0].schemaPath, "/items/properties/tags/items/type");
});
