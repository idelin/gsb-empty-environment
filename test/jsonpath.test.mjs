import assert from "node:assert/strict";
import test from "node:test";

import {
  evaluateJsonPath,
  jsonPathExists,
  parse,
  tryEvaluateJsonPath,
} from "../dist/index.js";

test("parses mode declarations and defaults to lax", () => {
  assert.equal(parse("$.a").mode, "lax");
  assert.equal(parse("lax $.a").mode, "lax");
  assert.equal(parse("strict $.a").mode, "strict");
  assert.equal(
    parse("$.a", { defaultMode: "strict" }).mode,
    "strict",
  );
  assert.equal(parse("lax $", { defaultMode: "strict" }).mode, "lax");
});

test("lax member access recursively unwraps nested arrays", () => {
  const document = {
    a: [[{ b: 1 }], [[{ b: 2 }]]],
  };
  assert.deepEqual(evaluateJsonPath("lax $.a.b", document), [1, 2]);
});

test("strict member access does not unwrap arrays", () => {
  assert.throws(
    () => evaluateJsonPath("strict $.a.b", { a: [{ b: 1 }] }),
    (error) => error.code === "MEMBER_ON_NON_OBJECT",
  );
});

test("lax suppresses missing keys and non-object member access", () => {
  assert.deepEqual(evaluateJsonPath("lax $.a.b", { a: 3 }), []);
  assert.deepEqual(evaluateJsonPath("lax $.missing", { a: 1 }), []);
});

test("strict reports missing keys and out-of-range indexes", () => {
  assert.throws(
    () => evaluateJsonPath("strict $.missing", { a: 1 }),
    (error) => error.code === "MISSING_KEY",
  );
  assert.throws(
    () => evaluateJsonPath("strict $[7]", [1, 2]),
    (error) => error.code === "INDEX_OUT_OF_BOUNDS",
  );
});

test("lax wraps scalar operands for array access", () => {
  assert.deepEqual(evaluateJsonPath("lax $.status[0]", { status: "paid" }), [
    "paid",
  ]);
  assert.deepEqual(evaluateJsonPath("lax $.status[*]", { status: "paid" }), [
    "paid",
  ]);
  assert.deepEqual(evaluateJsonPath("lax $.status[1]", { status: "paid" }), []);
});

test("supports last, arithmetic indexes, lists, and ranges", () => {
  const document = [10, 11, 12, 13, 14];
  assert.deepEqual(evaluateJsonPath("$[last]", document), [14]);
  assert.deepEqual(evaluateJsonPath("$[last - 1]", document), [13]);
  assert.deepEqual(evaluateJsonPath("$[last - 1 to last]", document), [
    13,
    14,
  ]);
  assert.deepEqual(evaluateJsonPath("$[0, 2 to 3]", document), [10, 12, 13]);
});

test("supports wildcard and recursive descendant access", () => {
  assert.deepEqual(evaluateJsonPath("$.obj.*", { obj: { a: 1, b: 2 } }), [
    1,
    2,
  ]);
  assert.deepEqual(evaluateJsonPath("$.**.b", { a: { b: 1 }, b: 2 }), [2, 1]);
});

test("strict filter errors become UNKNOWN and stay UNKNOWN under negation", () => {
  assert.deepEqual(evaluateJsonPath("strict $ ? (@.a == 1)", {}), []);
  assert.deepEqual(
    evaluateJsonPath("strict $ ? (!(@.a == 1))", {}),
    [],
  );
  assert.deepEqual(
    evaluateJsonPath("strict $ ? (!(@.a == 1))", { a: 2 }),
    [{ a: 2 }],
  );
  assert.deepEqual(
    evaluateJsonPath('strict $ ? (!(@.a == "1"))', { a: 1 }),
    [],
  );
});

test("lax missing values compare as empty sequences, not errors", () => {
  assert.deepEqual(
    evaluateJsonPath("lax $ ? (!(@.a == 1))", {}),
    [{}],
  );
});

test("filter arrays unwrap one level in lax mode only", () => {
  assert.deepEqual(evaluateJsonPath("lax $ ? (@ > 1)", [1, 2, 3]), [2, 3]);
  assert.deepEqual(evaluateJsonPath("strict $ ? (@ > 1)", [1, 2, 3]), []);
  assert.deepEqual(
    evaluateJsonPath("strict $[*] ? (@ > 1)", [1, 2, 3]),
    [2, 3],
  );
});

test("comparisons are existentially quantified over arrays and sequences", () => {
  assert.deepEqual(
    evaluateJsonPath("lax $ ? (@.a == 1)", { a: [2, 1] }),
    [{ a: [2, 1] }],
  );
  assert.deepEqual(
    evaluateJsonPath("lax $ ? (@.a != 1)", { a: [1, 2] }),
    [{ a: [1, 2] }],
  );
  assert.deepEqual(evaluateJsonPath("lax $ ? (@.a == 1)", { a: [] }), []);
  assert.deepEqual(
    evaluateJsonPath("lax $ ? (!(@.a[*] == 1))", { a: [] }),
    [{ a: [] }],
  );
});

test("supports type, size, and double methods", () => {
  assert.deepEqual(
    evaluateJsonPath('lax $ ? (@.a.type() == "array")', { a: [1] }),
    [{ a: [1] }],
  );
  assert.deepEqual(
    evaluateJsonPath("lax $ ? (@.a.size() == 2)", { a: [1, 2] }),
    [{ a: [1, 2] }],
  );
  assert.deepEqual(
    evaluateJsonPath("lax $ ? (@.a.size() == 1)", { a: "scalar" }),
    [{ a: "scalar" }],
  );
  assert.deepEqual(
    evaluateJsonPath("lax $ ? (@.a.double() > 1)", { a: "2.5" }),
    [{ a: "2.5" }],
  );
  assert.deepEqual(evaluateJsonPath("lax $.a.double()", { a: ["1", "2"] }), [
    1,
    2,
  ]);
  assert.throws(
    () => evaluateJsonPath("strict $.a.size()", { a: 1 }),
    (error) => error.code === "METHOD_ON_INVALID_TYPE",
  );
});

test("supports arithmetic, logical operators, and like_regex", () => {
  assert.deepEqual(
    evaluateJsonPath("lax $ ? (@.price * 2 >= 10 && @.active)", {
      price: 5,
      active: true,
    }),
    [{ price: 5, active: true }],
  );
  assert.deepEqual(
    evaluateJsonPath('lax $ ? (@.name like_regex "^ab" flag "i")', {
      name: "AB Value",
    }),
    [{ name: "AB Value" }],
  );
  assert.deepEqual(
    evaluateJsonPath(
      'strict $ ? (@.name like_regex "^ab" flag "i" || @.id == 7)',
      { name: "zzz", id: 7 },
    ),
    [{ name: "zzz", id: 7 }],
  );
});

test("tryEvaluateJsonPath returns structured failures", () => {
  const result = tryEvaluateJsonPath("strict $.a", { b: 1 });
  assert.equal(result.ok, false);
  assert.equal(result.values.length, 0);
  assert.equal(result.error.code, "MISSING_KEY");

  const parsed = tryEvaluateJsonPath("strict $.", {});
  assert.equal(parsed.ok, false);
  assert.equal(parsed.error.code, "PARSE_ERROR");
});

test("jsonPathExists accepts null and false values as existing", () => {
  assert.equal(jsonPathExists("$.a", { a: null }), true);
  assert.equal(jsonPathExists("$.a", { a: false }), true);
  assert.equal(jsonPathExists("$.a", {}), false);
});
