import test from "node:test";
import assert from "node:assert/strict";
import { Budget, BudgetExceededError, QuerySyntaxError, parseQuery } from "../src/index.ts";

test("parses field, index and wildcard segments", () => {
  const q = parseQuery("$.store.book[0].title");
  assert.deepEqual(q.segments, [
    { kind: "field", name: "store" },
    { kind: "field", name: "book" },
    { kind: "index", index: 0 },
    { kind: "field", name: "title" },
  ]);
  assert.deepEqual(parseQuery("$.*").segments, [{ kind: "wildcard" }]);
  assert.deepEqual(parseQuery("$[*]").segments, [{ kind: "wildcard" }]);
});

test("parses quoted keys including slashes", () => {
  const q = parseQuery(`$['a/b']["c~d"]`);
  assert.deepEqual(q.segments, [
    { kind: "field", name: "a/b" },
    { kind: "field", name: "c~d" },
  ]);
});

test("parses slices and negative indices", () => {
  assert.deepEqual(parseQuery("$.a[-1]").segments, [
    { kind: "field", name: "a" },
    { kind: "index", index: -1 },
  ]);
  assert.deepEqual(parseQuery("$.a[1:4]").segments, [
    { kind: "field", name: "a" },
    { kind: "slice", start: 1, end: 4, step: null },
  ]);
  assert.deepEqual(parseQuery("$.a[::-1]").segments, [
    { kind: "field", name: "a" },
    { kind: "slice", start: null, end: null, step: -1 },
  ]);
  assert.deepEqual(parseQuery("$.a[::2]").segments, [
    { kind: "field", name: "a" },
    { kind: "slice", start: null, end: null, step: 2 },
  ]);
});

test("parses recursive descent", () => {
  assert.deepEqual(parseQuery("$..author").segments, [
    { kind: "recursive", inner: { kind: "field", name: "author" } },
  ]);
  assert.deepEqual(parseQuery("$..*").segments, [
    { kind: "recursive", inner: { kind: "wildcard" } },
  ]);
  assert.deepEqual(parseQuery("$..book[2]").segments, [
    { kind: "recursive", inner: { kind: "field", name: "book" } },
    { kind: "index", index: 2 },
  ]);
  assert.deepEqual(parseQuery("$..[0]").segments, [
    { kind: "recursive", inner: { kind: "index", index: 0 } },
  ]);
});

test("parses filter expressions", () => {
  const q = parseQuery("$.book[?(@.price < 10 && @.category == 'fiction' || !@.isbn)]");
  const filter = q.segments[1];
  assert.equal(filter.kind, "filter");
  assert.deepEqual(parseQuery("$[?(@ >= 2)]").segments[0].kind, "filter");
  assert.deepEqual(parseQuery("$..book[?(@.isbn)].title").segments[1].kind, "filter");
});

test("reports syntax errors with positions", () => {
  assert.throws(() => parseQuery("store.book"), (e: unknown) => {
    assert.ok(e instanceof QuerySyntaxError);
    assert.equal(e.pos, 0);
    return true;
  });
  assert.throws(() => parseQuery("$.a["), QuerySyntaxError);
  assert.throws(() => parseQuery("$.a[1:2:0]"), /slice step/);
  assert.throws(() => parseQuery("$.."), QuerySyntaxError);
  assert.throws(() => parseQuery("$[?(@.a == )]"), QuerySyntaxError);
  let err: unknown;
  try {
    parseQuery("$.a xyz");
  } catch (e) {
    err = e;
  }
  assert.ok(err instanceof QuerySyntaxError && err.pos === 4);
});

test("parse budget is enforced", () => {
  assert.throws(
    () => parseQuery("$.store.book[0].title", new Budget({ parse: 4 })),
    (e: unknown) => e instanceof BudgetExceededError && e.kind === "parse",
  );
  const q = parseQuery("$.a", new Budget({ parse: 100 }));
  assert.equal(q.segments.length, 1);
});
