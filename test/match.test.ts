import assert from "node:assert/strict";
import test from "node:test";
import {
  equalTree,
  match,
  PatternError,
  substitute,
  t,
  v,
  validatePattern,
} from "../src/match.ts";

test("literal atoms and lists", () => {
  assert.deepEqual(match("a", "a"), { ok: true, bindings: [] });
  assert.deepEqual(match(["a", ["b", null]], ["a", ["b", null]]), {
    ok: true,
    bindings: [],
  });
  const r = match("a", "b");
  assert.equal(r.ok, false);
  if (!r.ok) {
    assert.equal(r.failure.reason, "atom-mismatch");
    assert.deepEqual(r.failure.path, []);
  }
});

test("kind mismatch in both directions", () => {
  const r1 = match(["a"], "a");
  assert.equal(r1.ok, false);
  if (!r1.ok) assert.equal(r1.failure.reason, "kind-mismatch");
  const r2 = match("a", ["a"]);
  assert.equal(r2.ok, false);
  if (!r2.ok) assert.equal(r2.failure.reason, "kind-mismatch");
});

test("list length mismatch", () => {
  const r = match(["a", "b"], ["a"]);
  assert.equal(r.ok, false);
  if (!r.ok) {
    assert.equal(r.failure.reason, "length-mismatch");
    assert.deepEqual(r.failure.path, []);
  }
  assert.equal(match([], []).ok, true);
  assert.equal(match([], ["a"]).ok, false);
});

test("single-node variable binds any node, including lists", () => {
  const r = match([v("x"), "b"], [["nested", 1], "b"]);
  assert.equal(r.ok, true);
  if (r.ok) assert.deepEqual(r.bindings, [["x", ["nested", 1]]]);
});

test("repeated variable must bind structurally equal values", () => {
  const okR = match([v("x"), [v("x")]], [["a", 1], [["a", 1]]]);
  assert.equal(okR.ok, true);
  if (okR.ok) assert.deepEqual(okR.bindings, [["x", ["a", 1]]]);

  const bad = match([v("x"), v("x")], ["a", "b"]);
  assert.equal(bad.ok, false);
  if (!bad.ok) {
    assert.equal(bad.failure.reason, "variable-conflict");
    assert.deepEqual(bad.failure.path, [1]);
  }
});

test("tail variable binds the rest of the list; empty tail is legal", () => {
  const r = match(["a", t("rest")], ["a", 1, 2, 3]);
  assert.equal(r.ok, true);
  if (r.ok) assert.deepEqual(r.bindings, [["rest", [1, 2, 3]]]);

  const empty = match(["a", t("rest")], ["a"]);
  assert.equal(empty.ok, true);
  if (empty.ok) assert.deepEqual(empty.bindings, [["rest", []]]);

  const whole = match([t("rest")], ["a", "b"]);
  assert.equal(whole.ok, true);
  if (whole.ok) assert.deepEqual(whole.bindings, [["rest", ["a", "b"]]]);

  const tooShort = match(["a", "b", t("rest")], ["a"]);
  assert.equal(tooShort.ok, false);
  if (!tooShort.ok) assert.equal(tooShort.failure.reason, "length-mismatch");
});

test("nested tail variables", () => {
  const r = match([["a", t("s")], "b"], [["a", 1, 2], "b"]);
  assert.equal(r.ok, true);
  if (r.ok) assert.deepEqual(r.bindings, [["s", [1, 2]]]);

  const deep = match([[t("s")]], [[1, 2, 3]]);
  assert.equal(deep.ok, true);
  if (deep.ok) assert.deepEqual(deep.bindings, [["s", [1, 2, 3]]]);
});

test("repeated tail variable must bind equal lists", () => {
  const okR = match([["a", t("s")], ["b", t("s")]], [["a", 1], ["b", 1]]);
  assert.equal(okR.ok, true);
  if (okR.ok) assert.deepEqual(okR.bindings, [["s", [1]]]);

  const bad = match([["a", t("s")], ["b", t("s")]], [["a", 1], ["b", 2]]);
  assert.equal(bad.ok, false);
  if (!bad.ok) {
    assert.equal(bad.failure.reason, "variable-conflict");
    assert.deepEqual(bad.failure.path, [1, 1]);
  }
});

test("mixed single and tail variables with the same name", () => {
  const r = match([v("x"), [t("x")]], [[], []]);
  assert.equal(r.ok, true);
  if (r.ok) assert.deepEqual(r.bindings, [["x", []]]);
  const bad = match([v("x"), [t("x")]], ["a", ["b"]]);
  assert.equal(bad.ok, false);
  if (!bad.ok) assert.equal(bad.failure.reason, "variable-conflict");
});

test("tail variable only allowed at the end of a list", () => {
  assert.throws(() => validatePattern([t("s"), "a"]), PatternError);
  assert.throws(() => validatePattern([[t("s"), v("x")]]), PatternError);
  assert.throws(() => validatePattern(t("s")), PatternError);
  assert.throws(() => match([t("s"), "a"], ["x", "a"]), PatternError);
  assert.doesNotThrow(() => validatePattern(["a", [t("s")]]));
});

test("failure path is the shallowest across siblings", () => {
  // Left child fails at depth 2, right child fails at depth 1: report [1].
  const r = match([["a", "b"], "q"], [["a", "x"], "r"]);
  assert.equal(r.ok, false);
  if (!r.ok) {
    assert.equal(r.failure.reason, "atom-mismatch");
    assert.deepEqual(r.failure.path, [1]);
  }
  // Deep conflict in an only-failing child is still reported precisely.
  const deep = match([v("x"), ["c", v("x")]], ["a", ["c", "b"]]);
  assert.equal(deep.ok, false);
  if (!deep.ok) assert.deepEqual(deep.failure.path, [1, 1]);
});

test("bindings are stable and in first-occurrence pre-order", () => {
  const pattern = [v("y"), [v("x"), t("s")], v("y")];
  const tree = ["z", [1, 2, 3], "z"];
  const r1 = match(pattern, tree);
  const r2 = match(pattern, tree);
  assert.equal(r1.ok, true);
  assert.deepEqual(JSON.stringify(r1), JSON.stringify(r2));
  if (r1.ok) {
    assert.deepEqual(r1.bindings.map(([n]) => n), ["y", "x", "s"]);
    assert.deepEqual(r1.bindings, [["y", "z"], ["x", 1], ["s", [2, 3]]]);
  }
});

test("substitute round-trips a successful match", () => {
  const pattern = [v("x"), ["a", t("rest")], v("x")];
  const tree = [[1], ["a", 2, 3], [1]];
  const r = match(pattern, tree);
  assert.equal(r.ok, true);
  if (r.ok) assert.equal(equalTree(substitute(pattern, r.bindings), tree), true);
});

test("substitute rejects unbound variables and non-list tails", () => {
  assert.throws(() => substitute(v("x"), []), PatternError);
  assert.throws(() => substitute([t("s")], [["s", "a"]]), PatternError);
  assert.throws(() => substitute(t("s"), [["s", []]]), PatternError);
});

test("structural equality treats distinct but equal trees as equal", () => {
  assert.equal(equalTree(["a", [1, null]], ["a", [1, null]]), true);
  assert.equal(equalTree(["a", [1]], ["a", [1, null]]), false);
  assert.equal(equalTree(0, false), false);
  assert.equal(equalTree(null, null), true);
});
