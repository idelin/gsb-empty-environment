import { test } from "node:test";
import assert from "node:assert/strict";
import {
  canonicalize,
  checkJsonCompatible,
  formatPath,
  hashJson,
} from "../src/json.ts";

test("object key order is insignificant for canonical form and hash", () => {
  const a = { x: 1, y: { b: 2, a: [1, 2] } };
  const b = { y: { a: [1, 2], b: 2 }, x: 1 };
  assert.equal(canonicalize(a), canonicalize(b));
  assert.equal(hashJson(a), hashJson(b));
});

test("array order is significant", () => {
  assert.notEqual(canonicalize([1, 2]), canonicalize([2, 1]));
  assert.notEqual(hashJson({ a: [1, 2] }), hashJson({ a: [2, 1] }));
});

test("formatPath renders object keys and array indices", () => {
  assert.equal(formatPath([]), "$");
  assert.equal(formatPath(["user", "tags", 2]), "$.user.tags[2]");
  assert.equal(formatPath(["odd key"]), '$["odd key"]');
});

test("checkJsonCompatible locates non-JSON values by path", () => {
  const issues = checkJsonCompatible({ ok: 1, bad: [undefined, { f: () => 1 }], nan: NaN });
  const paths = issues.map((i) => i.path).sort();
  assert.deepEqual(paths, ["$.bad[0]", "$.bad[1].f", "$.nan"]);
});

test("checkJsonCompatible accepts plain JSON trees", () => {
  assert.deepEqual(
    checkJsonCompatible({ a: [1, "x", null, true, { b: 2.5 }] }),
    [],
  );
});
