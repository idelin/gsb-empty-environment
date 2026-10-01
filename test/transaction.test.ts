import { test } from "node:test";
import assert from "node:assert/strict";
import { TemplateEngine } from "../src/engine.ts";
import { TemplateError, DataError } from "../src/errors.ts";
import type { JsonValue } from "../src/json.ts";
import { USERS_TEMPLATE, usersData } from "./helpers.ts";

test("transaction publishes output and provenance atomically", () => {
  const eng = new TemplateEngine(USERS_TEMPLATE);
  eng.setData(usersData());
  const r = eng.transaction((tx) => tx.set("app.title", "next"));
  assert.strictEqual((r.output as { title: string }).title, "NEXT");
  assert.strictEqual(eng.output, r.output);
  assert.strictEqual(eng.provenance, r.provenance);
  assert.ok(r.provenance.length > 0);
});

test("function failure rolls back data and keeps the previous output", () => {
  const tpl: JsonValue = {
    names: { $for: "users", $as: "u", $body: "${u.name | upper}" },
  };
  const eng = new TemplateEngine(tpl);
  const before = eng.setData({ users: [{ name: "a" }, { name: "b" }] });
  assert.deepStrictEqual(before.output, { names: ["A", "B"] });

  assert.throws(
    () => eng.transaction((tx) => tx.set("users.0.name", 42)),
    (err: unknown) => {
      assert.ok(err instanceof TemplateError);
      assert.strictEqual(err.templatePath, "/names/$body");
      assert.strictEqual(err.inputPath, "/users/0/name");
      assert.match(err.message, /upper: expected string, got number/);
      return true;
    },
  );

  // data rolled back
  assert.deepStrictEqual(eng.getData(), { users: [{ name: "a" }, { name: "b" }] });
  // previously published output and provenance retained
  assert.deepStrictEqual(eng.output, { names: ["A", "B"] });
  assert.deepStrictEqual(eng.provenance, before.provenance);
});

test("a failing op rolls back earlier ops of the same transaction", () => {
  const eng = new TemplateEngine(USERS_TEMPLATE);
  eng.setData(usersData());
  assert.throws(
    () =>
      eng.transaction((tx) => {
        tx.set("app.title", "changed");
        tx.remove("app.users", 0);
        tx.set("app.nope.deep", 1); // intermediate key missing -> DataError
      }),
    (err: unknown) => {
      assert.ok(err instanceof DataError);
      assert.strictEqual(err.path, "app.nope.deep");
      return true;
    },
  );
  const data = eng.getData() as { app: { title: string; users: unknown[] } };
  assert.strictEqual(data.app.title, "demo"); // set rolled back
  assert.strictEqual(data.app.users.length, 3); // remove rolled back
});

test("budget exhaustion inside a transaction rolls everything back", () => {
  const tpl: JsonValue = { list: { $for: "items", $as: "it", $body: "${it}" } };
  const eng = new TemplateEngine(tpl, { budget: { maxExpansions: 5 } });
  eng.setData({ items: [1, 2] });
  assert.throws(
    () =>
      eng.transaction((tx) => {
        for (let v = 3; v <= 8; v++) tx.insert("items", v - 1, v);
        // 6 brand-new entities -> 6 expansions > budget 5
      }),
    /expansion budget exceeded/,
  );
  assert.deepStrictEqual(eng.getData(), { items: [1, 2] });
  assert.deepStrictEqual(eng.output, { list: [1, 2] });
});

test("a failed transaction does not poison the fragment cache", () => {
  const tpl: JsonValue = {
    names: { $for: "users", $as: "u", $body: "${u.name | upper}" },
  };
  const eng = new TemplateEngine(tpl);
  eng.setData({ users: [{ name: "a" }, { name: "b" }] });
  assert.throws(() => eng.transaction((tx) => tx.set("users.0.name", 42)));

  const r = eng.transaction((tx) => tx.set("users.0.name", "ann"));
  assert.deepStrictEqual(r.output, { names: ["ANN", "B"] });
  const full = eng.fullRender();
  assert.deepStrictEqual(r.output, full.output);
  assert.deepStrictEqual(r.provenance, full.provenance);
});

test("concurrent-looking edits are isolated per transaction", () => {
  const eng = new TemplateEngine(USERS_TEMPLATE);
  eng.setData(usersData());
  const r1 = eng.transaction((tx) => tx.move("app.users", 0, 2));
  const r2 = eng.transaction((tx) => tx.remove("app.users", 0));
  assert.deepStrictEqual(
    (r2.output as { users: Array<{ name: string }> }).users.map((u) => u.name),
    ["cid", "ann"],
  );
  // r1 is a stale snapshot; engine state reflects only committed transactions
  assert.notDeepStrictEqual(r1.output, r2.output);
  assert.deepStrictEqual(eng.output, r2.output);
});
