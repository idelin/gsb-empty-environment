import { test } from "node:test";
import assert from "node:assert/strict";
import { ModuleRegistry } from "../src/registry.ts";
import { RuleBook } from "../src/rules.ts";
import { makeAttrs } from "../src/policy.ts";
import type { ModuleSource } from "../src/types.ts";

function ruleModule(module: string, condition: object): ModuleSource {
  return {
    name: module,
    version: "1.0.0",
    imports: [],
    exports: [
      {
        name: "denyGuest",
        kind: "decl",
        signature: "rule",
        decl: { type: "rule", effect: "deny", condition },
      },
    ],
  };
}

test("两个模块的同名规则互不覆盖，按命名空间各自求值", () => {
  const reg = new ModuleRegistry();
  reg.hotReplace([
    ruleModule("teamA", { op: "eq", attr: "level", value: 0 }),
    ruleModule("teamB", { op: "in", attr: "region", set: ["eu"] }),
  ]);
  const book = RuleBook.fromSnapshot(reg.snapshot());

  assert.equal(book.list().length, 2, "同名规则必须共存");
  assert.ok(book.get("teamA:denyGuest"));
  assert.ok(book.get("teamB:denyGuest"));

  const guest = makeAttrs({ level: 0, region: "us" });
  assert.equal(book.evaluate("teamA:denyGuest", guest), "deny", "teamA 命中 level=0");
  assert.equal(book.evaluate("teamB:denyGuest", guest), null, "teamB 的规则不受 teamA 影响");

  const euUser = makeAttrs({ level: 1, region: "eu" });
  assert.equal(book.evaluate("teamA:denyGuest", euUser), null);
  assert.equal(book.evaluate("teamB:denyGuest", euUser), "deny", "teamB 命中 region=eu");
});

test("规则条件在登记时即校验，非法条件被拒绝", () => {
  const reg = new ModuleRegistry();
  reg.hotReplace([ruleModule("teamC", { op: "regex", attr: "x" })]);
  assert.throws(() => RuleBook.fromSnapshot(reg.snapshot()), /不支持的运算/);
});
