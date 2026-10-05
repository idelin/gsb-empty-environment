import test from "node:test";
import assert from "node:assert/strict";
import {
  Budget,
  BudgetExceededError,
  encodePath,
  evaluate,
  parseQuery,
  type JsonValue,
} from "../src/index.ts";
import { deepTree, pathsOf, sorted, store } from "./helpers.ts";

function run(src: string, doc: JsonValue = store) {
  return evaluate(doc, parseQuery(src));
}

test("field and index selection", () => {
  assert.equal(run("$.store.book[0].title")[0].value, "Sayings of the Century");
  assert.equal(run("$.expensive")[0].value, 10);
  assert.deepEqual(pathsOf(run("$.store.book[0].title")), ["/store/book/0/title"]);
});

test("recursive descent with dedup", () => {
  const authors = run("$..author");
  assert.equal(authors.length, 4);
  const all = run("$..*");
  const keys = pathsOf(all);
  assert.equal(new Set(keys).size, keys.length, "no duplicate paths");
  const doc: JsonValue = { x: 1, a: { x: 2 } };
  assert.deepEqual(pathsOf(run("$..x", doc)), ["/x", "/a/x"]);
});

test("negative index and slices", () => {
  const doc: JsonValue = { arr: [1, 2, 3, 4, 5] };
  assert.equal(run("$.arr[-1]", doc)[0].value, 5);
  assert.deepEqual(run("$.arr[1:3]", doc).map((r) => r.value), [2, 3]);
  assert.deepEqual(run("$.arr[-2:]", doc).map((r) => r.value), [4, 5]);
  assert.deepEqual(run("$.arr[::-1]", doc).map((r) => r.value), [5, 4, 3, 2, 1]);
  assert.deepEqual(run("$.arr[::2]", doc).map((r) => r.value), [1, 3, 5]);
  assert.deepEqual(run("$.arr[3:1]", doc), []);
});

test("filters: comparison, logic, existence", () => {
  const cheap = run("$..book[?(@.price < 10)].title");
  assert.deepEqual(cheap.map((r) => r.value), ["Sayings of the Century", "Moby Dick"]);
  const withIsbn = run("$..book[?(@.isbn)].title");
  assert.deepEqual(withIsbn.map((r) => r.value), ["Moby Dick", "The Lord of the Rings"]);
  const noIsbn = run("$..book[?(!@.isbn)].title");
  assert.deepEqual(noIsbn.map((r) => r.value), ["Sayings of the Century", "Sword of Honour"]);
  const fiction = run("$..book[?(@.category == 'fiction' && @.price < 10)].title");
  assert.deepEqual(fiction.map((r) => r.value), ["Moby Dick"]);
  const either = run("$..book[?(@.price > 20 || @.category == 'reference')].title");
  assert.deepEqual(either.map((r) => r.value), ["Sayings of the Century", "The Lord of the Rings"]);
  const scalars = run("$.arr[?(@ >= 2)]", { arr: [1, 2, 3] });
  assert.deepEqual(scalars.map((r) => r.value), [2, 3]);
});

test("keys containing slashes and tildes", () => {
  const doc: JsonValue = { "a/b": { c: 1 }, "tilde~key": [10, 20], "": { " ": 7 } };
  assert.equal(run("$['a/b'].c", doc)[0].value, 1);
  assert.equal(run("$..['a/b'].c", doc)[0].value, 1);
  assert.equal(run("$['tilde~key'][1]", doc)[0].value, 20);
  assert.equal(run("$[''][' ']", doc)[0].value, 7);
  assert.equal(encodePath(["a/b", "c"]), "/a~1b/c");
  assert.equal(encodePath(["tilde~key"]), "/tilde~0key");
});

test("deep trees do not overflow the stack", () => {
  const doc = deepTree(3000);
  const results = run("$..a", doc);
  assert.equal(results.length, 3000);
  assert.equal(run("$", doc).length, 1);
  // deep field chain: linear output at depth 10000
  const chain = deepTree(10000);
  const q = "$" + ".a".repeat(10000);
  const r = run(q, chain);
  assert.equal(r.length, 1);
  assert.equal(r[0].value, 1);
  assert.equal(r[0].path.length, 10000);
});

test("empty results for non-matching queries", () => {
  assert.deepEqual(run("$.missing"), []);
  assert.deepEqual(run("$.store.book[99]"), []);
  assert.deepEqual(run("$..book[?(@.price > 1000)]"), []);
  assert.deepEqual(run("$.store.book.title"), []);
});

test("object key order does not affect results", () => {
  const a: JsonValue = { x: 1, y: { z: 2 }, w: [1, 2] };
  const b: JsonValue = { w: [1, 2], y: { z: 2 }, x: 1 };
  for (const src of ["$..*", "$..z", "$.w[*]", "$..x"]) {
    assert.deepEqual(sorted(run(src, a)), sorted(run(src, b)));
  }
});

test("expand and compare budgets are enforced", () => {
  assert.throws(
    () => evaluate(store, parseQuery("$..*"), new Budget({ expand: 5 })),
    (e: unknown) => e instanceof BudgetExceededError && e.kind === "expand",
  );
  assert.throws(
    () => evaluate(store, parseQuery("$..book[?(@.price < 10)]"), new Budget({ compare: 1 })),
    (e: unknown) => e instanceof BudgetExceededError && e.kind === "compare",
  );
});
