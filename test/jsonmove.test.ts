import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import {
  applyMoves,
  deepEqual,
  formatPath,
  JsonMoveError,
  parsePath,
  planMoves,
} from "../src/jsonmove.ts";

function capture(fn: () => unknown): JsonMoveError {
  try {
    fn();
  } catch (err) {
    assert.ok(err instanceof JsonMoveError, "expected JsonMoveError");
    return err as JsonMoveError;
  }
  assert.fail("expected the move to throw");
}

test("parsePath/formatPath: slash and backslash escaping round-trips", () => {
  assert.deepEqual(parsePath("a\\/b/c"), ["a/b", "c"]);
  assert.deepEqual(parsePath("a\\\\b"), ["a\\b"]);
  assert.deepEqual(parsePath("/a/b"), ["a", "b"]);
  assert.deepEqual(parsePath(""), []);
  assert.equal(formatPath(["a/b", "c"]), "/a\\/b/c");
  assert.equal(formatPath(["a\\b"]), "/a\\\\b");
  assert.equal(formatPath(parsePath("x/y\\/z")), "/x/y\\/z");
  assert.throws(() => parsePath("a\\q"), JsonMoveError);
});

test("deepEqual: object keys unordered, arrays ordered", () => {
  assert.ok(deepEqual({ a: 1, b: [1, 2] }, { b: [1, 2], a: 1 }));
  assert.ok(!deepEqual([1, 2], [2, 1]));
  assert.ok(!deepEqual({ a: 1 }, { a: 1, b: 2 }));
});

test("basic wildcard move a/* -> b/$1", () => {
  const out = applyMoves({ a: { x: 1, y: 2 } }, "a/*", "b/$1");
  assert.deepEqual(out, { a: {}, b: { x: 1, y: 2 } });
});

test("identity move is a no-op", () => {
  const input = { a: { x: 1 } };
  assert.deepEqual(applyMoves(input, "a/*", "a/$1"), input);
});

test("conflict: target exists with a different value, error carries path", () => {
  const input = { a: { x: 1 }, b: { x: 999 } };
  const err = capture(() => applyMoves(input, "a/*", "b/$1"));
  assert.equal(err.path, "/b/x");
  assert.match(err.message, /conflict/);
});

test("no conflict when target holds a deep-equal value (key order irrelevant)", () => {
  const input = { a: { x: { p: 1, q: 2 } }, b: { x: { q: 2, p: 1 } } };
  const out = applyMoves(input, "a/*", "b/$1");
  assert.deepEqual(out, { a: {}, b: { x: { q: 2, p: 1 } } });
});

test("conflict: two sources collapse onto one target with different values", () => {
  const input = { a: { x: 1 }, b: { x: 2 } };
  const err = capture(() => applyMoves(input, "*/x", "c"));
  assert.equal(err.path, "/c");
});

test("same target with same value merges cleanly", () => {
  const out = applyMoves({ a: { x: 1 }, b: { x: 1 } }, "*/x", "c");
  assert.deepEqual(out, { a: {}, b: {}, c: 1 });
});

test("keys containing slashes are matched via escaping", () => {
  const input = { "weird/key": { sub: true }, other: 0 };
  const out = applyMoves(input, "weird\\/key/*", "flat/$1");
  assert.deepEqual(out, { "weird/key": {}, other: 0, flat: { sub: true } });
  // the unescaped form must NOT match the compound key
  const plan = planMoves(input, "weird/key", "z");
  assert.equal(plan.length, 0);
});

test("arrays stay ordered: fan-out copy and append", () => {
  const input = { list: ["a", "b", "c"], extra: "z" };
  const out = applyMoves(input, "list/*", "copy/$1");
  assert.deepEqual(out, { list: [], extra: "z", copy: ["a", "b", "c"] });
  const out2 = applyMoves(input, "extra", "list/3");
  assert.deepEqual(out2, { list: ["a", "b", "c", "z"] });
});

test("moving a node into its own subtree terminates (a -> a/b)", () => {
  const out = applyMoves({ a: { x: 1 } }, "a", "a/b");
  assert.deepEqual(out, { a: { b: { x: 1 } } });
});

test("newly created nodes are not re-scanned (a/* -> a/sub/$1)", () => {
  const out = applyMoves({ a: { x: 1, y: 2 } }, "a/*", "a/sub/$1");
  assert.deepEqual(out, { a: { sub: { x: 1, y: 2 } } });
});

test("** captures multiple segments; $N reorders captures", () => {
  const input = { x: { a: { b: { c: 1 } } } };
  const out = applyMoves(input, "x/*/*/**", "y/$2/$1");
  assert.deepEqual(out, { x: { a: {} }, y: { b: { a: { c: 1 } } } });
});

test("** with zero segments moves the whole subtree", () => {
  const input = { src: { deep: { deeper: 1 } }, keep: 0 };
  const out = applyMoves(input, "src/**", "dst/$1");
  assert.deepEqual(out, { keep: 0, dst: { deep: { deeper: 1 } } });
});

test("outermost match wins: ** fans out top-level keys only", () => {
  const out = applyMoves({ a: { x: 1 }, b: 2 }, "**", "z/$1");
  assert.deepEqual(out, { z: { a: { x: 1 }, b: 2 } });
});

test("target path through a scalar is a located error", () => {
  const err = capture(() => applyMoves({ a: 1, x: 5 }, "x", "a/b/c"));
  assert.equal(err.path, "/a");
});

test("target nested inside another planned target conflicts", () => {
  // x/a -> r (empty ** capture) and x/b -> r/c: r/c lies inside r
  const input = { x: { a: 1, b: { c: 2 } } };
  const err = capture(() => applyMoves(input, "x/*/**", "r/$2"));
  assert.equal(err.path, "/r");
});

test("keys shadowing Object.prototype names are safe", () => {
  const input = JSON.parse('{"constructor": {"x": 1}}');
  const out = applyMoves(input, "constructor/*", "c/$1");
  assert.deepEqual(out, { constructor: {}, c: { x: 1 } });
});

test("replacement referencing a missing capture is a located error", () => {
  const err = capture(() => applyMoves({ a: { x: 1 } }, "a/*", "b/$2"));
  assert.equal(err.path, "b/$2");
});

test("planMoves reports the plan before anything is applied", () => {
  const plan = planMoves({ a: { x: 1 }, b: { y: 2 } }, "*/*", "out/$1");
  assert.deepEqual(
    plan.map((e) => [formatPath(e.from), formatPath(e.to)]),
    [
      ["/a/x", "/out/a"],
      ["/b/y", "/out/b"],
    ],
  );
});

test("input tree is not mutated by applyMoves", () => {
  const input = { a: { x: 1 } };
  applyMoves(input, "a", "b");
  assert.deepEqual(input, { a: { x: 1 } });
});

test("CLI end-to-end: move, and conflict exit code", () => {
  const ok = execFileSync(
    process.execPath,
    ["src/cli.ts", "a/*", "b/$1", "sample/tree.json"],
    { encoding: "utf8" },
  );
  const parsed = JSON.parse(ok);
  assert.deepEqual(parsed.a, {});
  assert.deepEqual(parsed.b, { y: 2, x: 1 });

  let err: { status: number; stderr: string } | undefined;
  try {
    execFileSync(
      process.execPath,
      ["src/cli.ts", "a/*", "b/$1", "sample/conflict.json"],
      { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] },
    );
  } catch (e) {
    err = e as { status: number; stderr: string };
  }
  assert.ok(err, "expected the CLI to exit non-zero");
  assert.equal(err.status, 1);
  assert.match(err.stderr, /conflict/);
  assert.match(err.stderr, /\/b\/x/);
});
