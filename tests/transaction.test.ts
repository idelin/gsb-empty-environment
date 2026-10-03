import { test } from "node:test";
import assert from "node:assert/strict";
import { Engine, BudgetError, TemplateError } from "../src/index.ts";

test("commit publishes output and provenance atomically", () => {
  const eng = new Engine({ v: "${x}" }, { x: 1 });
  const outBefore = eng.output;
  const provBefore = eng.provenance;
  const r = eng.begin().set("/x", 2).commit();
  assert.equal(r.output.v, 2);
  assert.equal(eng.output.v, 2);
  assert.notEqual(eng.provenance, provBefore);
  assert.deepEqual(Object.keys(eng.provenance), Object.keys(provBefore));
  assert.notEqual(eng.output, outBefore);
});

test("function failure rolls back input mutations, output and provenance", () => {
  const eng = new Engine(
    { ratio: "${div(total, count)}", items: { $for: "items", as: "i", body: "${@i}" } },
    { total: 10, count: 2, items: [1, 2] },
  );
  const outBefore = structuredClone(eng.output);
  const provBefore = JSON.stringify(eng.provenance);
  const dataBefore = JSON.stringify(eng.store.toJson());
  assert.throws(
    () =>
      eng
        .begin()
        .set("/count", 0) // div by zero will fire during render
        .insert("/items", 0, 99)
        .commit(),
    (e: unknown) => e instanceof TemplateError && /division by zero/.test((e as Error).message),
  );
  assert.deepEqual(eng.output, outBefore);
  assert.equal(JSON.stringify(eng.provenance), provBefore);
  assert.equal(JSON.stringify(eng.store.toJson()), dataBefore);
  // engine still usable afterwards
  eng.begin().set("/count", 5).commit();
  assert.equal(eng.output.ratio, 2);
  assert.ok(eng.verify());
});

test("unknown function (not whitelisted) fails the whole transaction", () => {
  const eng = new Engine({ x: "${a}" }, { a: 1 });
  const dataBefore = JSON.stringify(eng.store.toJson());
  // simulate a template-time safe state, then swap in a bad template via options
  const bad = new Engine(
    { x: "${a}" },
    { a: 1 },
  );
  assert.throws(() => {
    // direct: unknown function at construction
    new Engine({ x: "${hack(a)}" }, { a: 1 });
  }, TemplateError);
  assert.equal(JSON.stringify(eng.store.toJson()), dataBefore);
  assert.equal(JSON.stringify(bad.store.toJson()), JSON.stringify({ a: 1 }));
});

test("budget exceeded inside a transaction rolls everything back", () => {
  const eng = new Engine(
    { items: { $for: "items", as: "i", body: "${upper(@i)}" } },
    { items: ["a"] },
    { maxExpansions: 10 },
  );
  const dataBefore = JSON.stringify(eng.store.toJson());
  const outBefore = structuredClone(eng.output);
  assert.throws(
    () => eng.begin().set("/items", ["a", "b", "c", "d", "e", "f", "g", "h"]).commit(),
    BudgetError,
  );
  assert.equal(JSON.stringify(eng.store.toJson()), dataBefore);
  assert.deepEqual(eng.output, outBefore);
});

test("manual rollback discards mutations", () => {
  const eng = new Engine({ x: "${a}" }, { a: 1 });
  const tx = eng.begin();
  tx.set("/a", 99);
  tx.rollback();
  assert.equal(eng.output.x, 1);
  assert.equal(JSON.stringify(eng.store.toJson()), JSON.stringify({ a: 1 }));
  assert.throws(() => tx.commit(), /already closed/);
});

test("missing-path error inside transaction rolls back and carries paths", () => {
  const eng = new Engine({ x: "${user.name}" }, { user: { name: "a" } });
  try {
    eng.begin().remove("/user/name").commit();
    assert.fail("should throw");
  } catch (e) {
    const err = e as { tpath: string; ipath: string };
    assert.equal(err.tpath, "/x");
    assert.equal(err.ipath, "/user/name");
  }
  assert.equal(eng.output.x, "a");
  assert.deepEqual(eng.store.toJson(), { user: { name: "a" } });
});
