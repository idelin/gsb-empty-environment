import { test } from "node:test";
import assert from "node:assert/strict";
import { TemplateEngine } from "../src/engine.ts";
import { TemplateError } from "../src/errors.ts";
import type { JsonValue } from "../src/json.ts";
import { USERS_TEMPLATE, usersData } from "./helpers.ts";

test("interpolates paths; single-expression strings preserve JSON types", () => {
  const eng = new TemplateEngine({
    n: "${a.n}",
    s: "x${a.n}y",
    arr: "${a.list}",
    nil: "${a.nil}",
    bool: "${a.b}",
  });
  const { output } = eng.setData({ a: { n: 42, list: [1, "two"], nil: null, b: true } });
  const o = output as Record<string, JsonValue>;
  assert.strictEqual(o.n, 42);
  assert.strictEqual(o.s, "x42y");
  assert.deepStrictEqual(o.arr, [1, "two"]);
  assert.strictEqual(o.nil, null);
  assert.strictEqual(o.bool, true);
});

test("escaped \\${ keeps a literal placeholder", () => {
  const eng = new TemplateEngine({ msg: "cost: \\${a.n} vs ${a.n}", raw: "\\${}" });
  const { output } = eng.setData({ a: { n: 7 } });
  assert.strictEqual((output as Record<string, JsonValue>).msg, "cost: ${a.n} vs 7");
  assert.strictEqual((output as Record<string, JsonValue>).raw, "${}");
});

test("nested loops expand in order", () => {
  const eng = new TemplateEngine({
    grid: { $for: "rows", $as: "r", $body: { $for: "r.cells", $as: "c", $body: "${c}" } },
  });
  const { output } = eng.setData({ rows: [{ cells: [1, 2] }, { cells: [3] }, { cells: [] }] });
  assert.deepStrictEqual((output as Record<string, JsonValue>).grid, [[1, 2], [3], []]);
});

test("conditionals follow truthiness rules", () => {
  const tpl: JsonValue = {
    $for: "vals",
    $as: "v",
    $body: { $if: "v", $then: "T", $else: "F" },
  };
  const eng = new TemplateEngine(tpl);
  const { output } = eng.setData({ vals: [false, 0, "", null, true, 1, "x", [], {}] });
  assert.deepStrictEqual(output, ["F", "F", "F", "F", "T", "T", "T", "T", "T"]);
});

test("missing in a condition is falsy; missing policy 'empty' renders ''", () => {
  const eng = new TemplateEngine({
    a: "v=${nope}",
    b: "${nope}",
    c: { $if: "nope", $then: "T", $else: "F" },
  });
  const o = eng.setData({}).output as Record<string, JsonValue>;
  assert.strictEqual(o.a, "v=");
  assert.strictEqual(o.b, "");
  assert.strictEqual(o.c, "F");
});

test("missing policy 'null' renders null for typed expressions", () => {
  const eng = new TemplateEngine({ b: "${nope}", a: "v=${nope}" }, { onMissing: "null" });
  const o = eng.setData({}).output as Record<string, JsonValue>;
  assert.strictEqual(o.b, null);
  assert.strictEqual(o.a, "v=");
});

test("missing policy 'error' throws a locatable TemplateError", () => {
  const eng = new TemplateEngine({ b: "${nope}" }, { onMissing: "error" });
  assert.throws(() => eng.setData({}), (err: unknown) => {
    assert.ok(err instanceof TemplateError);
    assert.strictEqual(err.templatePath, "/b");
    assert.match(err.message, /nope/);
    return true;
  });
});

test("explicit null is distinct from missing: default() only rescues missing", () => {
  const eng = new TemplateEngine({
    fromNull: "${present | default('D')}",
    fromMissing: "${absent | default('D')}",
    text: "[${present}][${absent}]",
  });
  const o = eng.setData({ present: null }).output as Record<string, JsonValue>;
  assert.strictEqual(o.fromNull, null); // explicit null passes through default()
  assert.strictEqual(o.fromMissing, "D"); // missing is substituted
  assert.strictEqual(o.text, "[null][]"); // null stringifies, missing vanishes
});

test("whitelisted functions: upper/lower/trim/join/length/json/first/last/not/eq", () => {
  const eng = new TemplateEngine({
    up: "${s | upper}",
    lo: "${s | lower}",
    tr: "${t | trim}",
    jo: "${list | join('-')}",
    len: "${list | length}",
    js: "${obj | json}",
    fi: "${list | first}",
    la: "${list | last}",
    nt: "${flag | not}",
    eq: "${s | eq('Hello')}",
    chain: "${t | trim | upper}",
  });
  const o = eng.setData({
    s: "Hello",
    t: "  pad  ",
    list: ["a", "b", "c"],
    obj: { k: 1 },
    flag: false,
  }).output as Record<string, JsonValue>;
  assert.strictEqual(o.up, "HELLO");
  assert.strictEqual(o.lo, "hello");
  assert.strictEqual(o.tr, "pad");
  assert.strictEqual(o.jo, "a-b-c");
  assert.strictEqual(o.len, 3);
  assert.strictEqual(o.js, '{"k":1}');
  assert.strictEqual(o.fi, "a");
  assert.strictEqual(o.la, "c");
  assert.strictEqual(o.nt, true);
  assert.strictEqual(o.eq, true);
  assert.strictEqual(o.chain, "PAD");
});

test("input object key order does not affect output", () => {
  const tpl: JsonValue = { x: "${a.x}", y: "${a.y}", z: "lit" };
  const eng1 = new TemplateEngine(tpl);
  const eng2 = new TemplateEngine(tpl);
  const o1 = eng1.setData({ a: { x: 1, y: 2 } }).output;
  const o2 = eng2.setData({ a: { y: 2, x: 1 } }).output;
  assert.deepStrictEqual(o1, o2);
  assert.strictEqual(JSON.stringify(o1), JSON.stringify(o2));
});

test("provenance maps output nodes to template and input paths", () => {
  const eng = new TemplateEngine({ msg: "hi ${user.name}", count: "${user.count}" });
  const { provenance } = eng.setData({ user: { name: "ann", count: 3 } });
  const byOut = new Map(provenance.map((p) => [p.out, p]));
  assert.strictEqual(byOut.get("/msg")?.template, "/msg");
  assert.deepStrictEqual(byOut.get("/msg")?.input, ["/user/name"]);
  assert.strictEqual(byOut.get("/count")?.template, "/count");
  assert.deepStrictEqual(byOut.get("/count")?.input, ["/user/count"]);
  assert.strictEqual(byOut.get("")?.template, ""); // root object
});

test("$index exposes the loop index; $root reaches the root from a loop", () => {
  const eng = new TemplateEngine({
    list: { $for: "items", $as: "it", $index: "i", $body: "${i}:${it}${$root.sep}" },
  });
  const { output } = eng.setData({ items: ["a", "b", "c"], sep: "!" });
  assert.deepStrictEqual((output as Record<string, JsonValue>).list, ["0:a!", "1:b!", "2:c!"]);
});

test("partials render via $ref in the current scope", () => {
  const eng = new TemplateEngine(
    { rows: { $for: "items", $as: "it", $body: { $ref: "row" } } },
    { partials: { row: { name: "${it.name}", n: "${it.n}" } } },
  );
  const { output } = eng.setData({ items: [{ name: "a", n: 1 }, { name: "b", n: 2 }] });
  assert.deepStrictEqual((output as Record<string, JsonValue>).rows, [
    { name: "a", n: 1 },
    { name: "b", n: 2 },
  ]);
});

test("standard template renders expected tree", () => {
  const eng = new TemplateEngine(USERS_TEMPLATE);
  const { output } = eng.setData(usersData());
  assert.deepStrictEqual(output, {
    title: "DEMO",
    users: [
      { name: "ann", tag: "x", admin: "yes", hobbies: ["a", "b"] },
      { name: "bob", tag: "none", admin: "no", hobbies: ["c"] },
      { name: "cid", tag: null, admin: "no", hobbies: [] },
    ],
  });
});
