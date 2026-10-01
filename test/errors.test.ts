import { test } from "node:test";
import assert from "node:assert/strict";
import { TemplateEngine } from "../src/engine.ts";
import { TemplateError } from "../src/errors.ts";
import type { JsonValue } from "../src/json.ts";

test("unknown function is rejected at parse time with template path", () => {
  assert.throws(
    () => new TemplateEngine({ a: "${x | hack}" }),
    (err: unknown) => {
      assert.ok(err instanceof TemplateError);
      assert.strictEqual(err.templatePath, "/a");
      assert.match(err.message, /unknown function "hack"/);
      assert.match(err.message, /whitelist/);
      return true;
    },
  );
});

test("unknown directive is rejected with template path", () => {
  assert.throws(
    () => new TemplateEngine({ a: { $foo: 1 } }),
    (err: unknown) => {
      assert.ok(err instanceof TemplateError);
      assert.strictEqual(err.templatePath, "/a");
      assert.match(err.message, /unknown directive "\$foo"/);
      return true;
    },
  );
});

test("$for requires $body; $if requires $then", () => {
  assert.throws(() => new TemplateEngine({ a: { $for: "x" } }), /\$for requires \$body/);
  assert.throws(() => new TemplateEngine({ a: { $if: "x" } }), /\$if requires \$then/);
});

test("unterminated interpolation and invalid paths are parse errors", () => {
  assert.throws(() => new TemplateEngine({ a: "${x" }), /unterminated/);
  assert.throws(() => new TemplateEngine({ a: "${x..y}" }), /invalid path/);
});

test("function type failure is a render error locating template and input", () => {
  const eng = new TemplateEngine({ a: "${n | upper}" });
  assert.throws(
    () => eng.setData({ n: 5 }),
    (err: unknown) => {
      assert.ok(err instanceof TemplateError);
      assert.strictEqual(err.templatePath, "/a");
      assert.strictEqual(err.inputPath, "/n");
      assert.match(err.message, /upper: expected string, got number/);
      return true;
    },
  );
});

test("$for on a non-array throws with the input path", () => {
  const eng = new TemplateEngine({ l: { $for: "x", $body: "${item}" } });
  assert.throws(
    () => eng.setData({ x: 5 }),
    (err: unknown) => {
      assert.ok(err instanceof TemplateError);
      assert.strictEqual(err.inputPath, "/x");
      assert.match(err.message, /not an array/);
      return true;
    },
  );
});

test("expansion budget is shared across all loops in a render", () => {
  const tpl: JsonValue = {
    a: { $for: "xs", $as: "x", $body: "${x}" },
    b: { $for: "ys", $as: "y", $body: "${y}" },
  };
  const eng = new TemplateEngine(tpl, { budget: { maxExpansions: 4 } });
  assert.throws(
    () => eng.setData({ xs: [1, 2, 3], ys: [4, 5, 6] }),
    /expansion budget exceeded/,
  );
  // fits exactly when the budget covers both loops
  const ok = new TemplateEngine(tpl, { budget: { maxExpansions: 6 } });
  assert.deepStrictEqual(ok.setData({ xs: [1, 2, 3], ys: [4, 5, 6] }).output, {
    a: [1, 2, 3],
    b: [4, 5, 6],
  });
});

test("nested loops multiply against the same shared budget", () => {
  const tpl: JsonValue = {
    g: { $for: "rows", $as: "r", $body: { $for: "r.c", $as: "x", $body: "${x}" } },
  };
  const eng = new TemplateEngine(tpl, { budget: { maxExpansions: 5 } });
  assert.throws(
    () => eng.setData({ rows: [{ c: [1, 2] }, { c: [3, 4] }] }), // 2 + 4 = 6 expansions
    /expansion budget exceeded/,
  );
});

test("recursive partials are stopped by the depth budget", () => {
  const eng = new TemplateEngine(
    { $ref: "loop" },
    { partials: { loop: { $ref: "loop" } }, budget: { maxDepth: 25 } },
  );
  assert.throws(() => eng.setData({}), /max template depth 25 exceeded/);
});

test("output explosion is stopped by the output-node budget", () => {
  const eng = new TemplateEngine(
    { copy: "${big}" },
    { budget: { maxOutputNodes: 10 } },
  );
  assert.throws(
    () => eng.setData({ big: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10] }),
    /output budget exceeded/,
  );
});

test("unknown partial reference is a render error", () => {
  const eng = new TemplateEngine({ $ref: "nope" });
  assert.throws(() => eng.setData({}), /unknown partial "nope"/);
});
