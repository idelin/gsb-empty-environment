import { test } from "node:test";
import assert from "node:assert/strict";
import {
  Engine,
  BudgetError,
  MissingError,
  TemplateError,
  TemplateCompileError,
} from "../src/index.ts";

test("interpolation, literals and type preservation", () => {
  const eng = new Engine(
    {
      text: "Hello ${user.name}, you have ${user.age} years",
      age: "${user.age}",
      tags: "${user.tags}",
      pi: 3.14,
      yes: true,
    },
    { user: { name: "Ada", age: 36, tags: ["a", "b"] } },
  );
  assert.equal(eng.output.text, "Hello Ada, you have 36 years");
  assert.equal(eng.output.age, 36); // single ${...} keeps the type
  assert.deepEqual(eng.output.tags, ["a", "b"]);
  assert.equal(eng.output.pi, 3.14);
  assert.equal(eng.output.yes, true);
});

test("\\${ escapes keep literal text, \\\\ escapes backslash", () => {
  const eng = new Engine(
    { a: "literal \\${not.evaluated} here", b: "back\\\\slash", c: "\\${x}=${add(1, 1)}" },
    {},
  );
  assert.equal(eng.output.a, "literal ${not.evaluated} here");
  assert.equal(eng.output.b, "back\\slash");
  assert.equal(eng.output.c, "${x}=2");
});

test("objects, arrays and nested loops with @index", () => {
  const eng = new Engine(
    {
      grid: {
        $for: "rows",
        as: "row",
        index: "r",
        body: {
          $for: "@row",
          as: "cell",
          index: "c",
          body: "${@r}:${@c}=${@cell}",
        },
      },
    },
    { rows: [["a", "b"], ["c"]] },
  );
  assert.deepEqual(eng.output.grid, [["0:0=a", "0:1=b"], ["1:0=c"]]);
});

test("$if then/else; missing else omits the key", () => {
  const eng = new Engine(
    {
      admin: { $if: "user.admin", then: "yes", else: "no" },
      maybe: { $if: "user.admin", then: "shown" },
      list: [{ $if: "user.admin", then: "x" }, "tail"],
    },
    { user: { admin: false } },
  );
  assert.equal(eng.output.admin, "no");
  assert.equal("maybe" in eng.output, false);
  assert.deepEqual(eng.output.list, ["tail"]);
});

test("missing vs explicit null are distinct", () => {
  const eng = new Engine(
    { a: "${user.nick}", b: { $get: "user.age", missing: "null" } },
    { user: { nick: null } }, // nick present but null; age missing
    { onMissing: "empty" },
  );
  assert.equal(eng.output.a, null); // explicit null renders as null, policy not triggered
  assert.equal(eng.output.b, null); // missing + policy null
});

test("missing policies: null / empty / omit / keep / error", () => {
  const tpl = {
    n: { $get: "nope", missing: "null" },
    e: { $get: "nope", missing: "empty" },
    k: { $get: "nope", missing: "keep" },
  };
  const eng = new Engine(tpl, {});
  assert.deepEqual(eng.output, { n: null, e: "", k: "${nope}" });

  const omit = new Engine({ a: { $get: "nope", missing: "omit" }, b: 1 }, {});
  assert.deepEqual(omit.output, { b: 1 });

  assert.throws(() => new Engine({ x: "${nope}" }, {}), MissingError);
});

test("missing error carries template path and input path", () => {
  try {
    new Engine({ deep: { down: "${user.profile.name}" } }, { user: {} });
    assert.fail("should have thrown");
  } catch (e) {
    assert.ok(e instanceof MissingError);
    assert.equal(e.tpath, "/deep/down");
    assert.equal(e.ipath, "/user/profile");
  }
});

test("whitelisted functions and nested calls", () => {
  const eng = new Engine(
    {
      shout: "${upper(trim(user.name))}",
      joined: "${join(user.tags, ' | ')}",
      total: "${add(1, 2, 3)}",
      label: "${fmt('user {0} has {1} tags', user.name, len(user.tags))}",
      nick: "${default(user.nick, 'anon')}",
      hasNick: "${defined(user.nick)}",
      cond: "${and(gt(user.age, 18), lt(user.age, 65))}",
    },
    { user: { name: "  ada ", tags: ["x", "y"], age: 36 } },
  );
  assert.equal(eng.output.shout, "ADA");
  assert.equal(eng.output.joined, "x | y");
  assert.equal(eng.output.total, 6);
  assert.equal(eng.output.label, "user   ada  has 2 tags");
  assert.equal(eng.output.nick, "anon");
  assert.equal(eng.output.hasNick, false);
  assert.equal(eng.output.cond, true);
});

test("unknown function is rejected with template path", () => {
  try {
    new Engine({ x: "${hack(user)}" }, { user: 1 });
    assert.fail("should have thrown");
  } catch (e) {
    assert.ok(e instanceof TemplateError);
    assert.match(e.message, /unknown function 'hack'/);
    assert.equal(e.tpath, "/x");
  }
});

test("function failure (div by zero) raises a located error", () => {
  try {
    new Engine({ x: "${div(1, 0)}" }, {});
    assert.fail("should have thrown");
  } catch (e) {
    assert.ok(e instanceof TemplateError);
    assert.match(e.message, /division by zero/);
    assert.equal(e.tpath, "/x");
  }
});

test("shared expansion budget covers loops AND function calls", () => {
  // 3 loop iterations + 3 upper() calls = 6 expansions; budget 5 must fail.
  assert.throws(
    () =>
      new Engine(
        { names: { $for: "users", as: "u", body: "${upper(@u)}" } },
        { users: ["a", "b", "c"] },
        { maxExpansions: 5 },
      ),
    BudgetError,
  );
  const ok = new Engine(
    { names: { $for: "users", as: "u", body: "${upper(@u)}" } },
    { users: ["a", "b", "c"] },
    { maxExpansions: 6 },
  );
  assert.deepEqual(ok.output.names, ["A", "B", "C"]);
});

test("output explosion is capped by output node budget", () => {
  assert.throws(
    () =>
      new Engine(
        { big: { $for: "items", as: "x", body: { a: "${@x}", b: "${@x}" } } },
        { items: [1, 2, 3, 4, 5] },
        { maxOutputNodes: 10 },
      ),
    BudgetError,
  );
});

test("template nesting depth is capped (recursive template guard)", () => {
  let deep: unknown = "leaf";
  for (let i = 0; i < 50; i++) deep = [deep];
  assert.throws(() => new Engine(deep as never, {}, { maxDepth: 20 }), BudgetError);
  const ok = new Engine(deep as never, {}, { maxDepth: 100 });
  assert.ok(ok.output !== null);
});

test("unclosed interpolation and unknown directives are compile errors", () => {
  assert.throws(() => new Engine({ x: "${oops" }, {}), TemplateCompileError);
  assert.throws(() => new Engine({ x: { $evil: 1 } }, {}), TemplateCompileError);
});

test("$call and $lit directive forms", () => {
  const eng = new Engine(
    {
      a: { $call: "upper", args: ["user.name"] },
      b: { $call: "fmt", args: [{ $lit: "[{0}]" }, "user.name"] },
    },
    { user: { name: "ada" } },
  );
  assert.equal(eng.output.a, "ADA");
  assert.equal(eng.output.b, "[ada]");
});
