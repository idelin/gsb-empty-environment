import { test } from "node:test";
import assert from "node:assert/strict";
import { ModuleRegistry } from "../src/registry.ts";
import { LinkFailure } from "../src/errors.ts";
import type { ModuleSource } from "../src/types.ts";

const value = (name: string, v: string) => ({
  name: "v",
  kind: "value" as const,
  signature: "string",
  make: () => `${name}@${v}`,
});

function world(version: string, initThrows = false): ModuleSource[] {
  return [
    {
      name: "b",
      version,
      imports: [],
      exports: [value("b", version)],
      init: initThrows
        ? () => {
            throw new Error("boom");
          }
        : undefined,
    },
    {
      name: "a",
      version,
      imports: [{ module: "b", version, names: [{ name: "v" }], loc: "a.ts:1:1" }],
      exports: [{ name: "fromB", kind: "value", signature: "string", make: (ctx) => ctx.get("v") as string }],
    },
  ];
}

test("初始化失败：整批链接失败，注册表不发生部分换版", () => {
  const reg = new ModuleRegistry();
  reg.hotReplace(world("1.0.0"));
  assert.equal(reg.snapshot().get("a")?.version, "1.0.0");

  assert.throws(
    () => reg.hotReplace(world("2.0.0", true)),
    (e: unknown) => (e as LinkFailure).errors[0].code === "INIT_FAILURE",
  );
  const snap = reg.snapshot();
  assert.equal(snap.get("a")?.version, "1.0.0", "a 不得被部分换版");
  assert.equal(snap.get("b")?.version, "1.0.0", "b 不得被部分换版");
});

test("签名检查失败：同样不允许部分模块换版", () => {
  const reg = new ModuleRegistry();
  reg.hotReplace(world("1.0.0"));
  const bad: ModuleSource[] = [
    { name: "b", version: "2.0.0", imports: [], exports: [value("b", "2.0.0")] },
    {
      name: "a",
      version: "2.0.0",
      imports: [{ module: "b", version: "2.0.0", names: [{ name: "missing" }], loc: "a.ts:9:1" }],
      exports: [],
    },
  ];
  assert.throws(
    () => reg.hotReplace(bad),
    (e: unknown) => (e as LinkFailure).errors[0].code === "MISSING_EXPORT",
  );
  assert.equal(reg.snapshot().get("a")?.version, "1.0.0");
  assert.equal(reg.snapshot().get("b")?.version, "1.0.0");
});

test("热替换与存活决策快照：旧异步属性返回旧版本的值", async () => {
  const delay = (ms: number) => new Promise((r) => setTimeout(r, ms));
  const configWorld = (version: string, profile: string): ModuleSource[] => [
    {
      name: "config",
      version,
      imports: [],
      exports: [
        {
          name: "getProfile",
          kind: "value",
          signature: "() => Promise<string>",
          make: () => async () => {
            await delay(30);
            return profile;
          },
        },
      ],
    },
  ];
  const reg = new ModuleRegistry();
  reg.hotReplace(configWorld("1.0.0", "v1-profile"));

  // 决策进行中：基于旧快照发起异步属性读取
  const snap1 = reg.snapshot();
  const getProfile1 = snap1.get("config")?.exports.get("getProfile") as () => Promise<string>;
  const pending = getProfile1();

  // 读取尚未完成时发生热替换
  reg.hotReplace(configWorld("2.0.0", "v2-profile"));

  assert.equal(await pending, "v1-profile", "旧异步属性必须返回旧版本的值");
  assert.equal(snap1.get("config")?.version, "1.0.0", "旧快照仍然存活");

  const snap2 = reg.snapshot();
  const getProfile2 = snap2.get("config")?.exports.get("getProfile") as () => Promise<string>;
  assert.equal(await getProfile2(), "v2-profile", "新快照使用新版本");
  assert.notEqual(snap1.vector, snap2.vector);
});
