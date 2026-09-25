import { test } from "node:test";
import assert from "node:assert/strict";
import { link, buildDepGraphs } from "../src/linker.ts";
import { LinkFailure } from "../src/errors.ts";
import { shortestCycle } from "../src/graph.ts";
import type { ModuleSource } from "../src/types.ts";

test("菱形共享导入：D 只实例化一次，B 与 C 看到同一实例", () => {
  let dEvalCount = 0;
  const d: ModuleSource = {
    name: "d",
    version: "1.0.0",
    imports: [],
    exports: [
      { name: "token", kind: "value", signature: "object", make: () => { dEvalCount += 1; return { id: "d-token" }; } },
    ],
  };
  const b: ModuleSource = {
    name: "b",
    version: "1.0.0",
    imports: [{ module: "d", version: "1.0.0", names: [{ name: "token" }], loc: "b.ts:1:1" }],
    exports: [{ name: "bToken", kind: "value", signature: "object", make: (ctx) => ctx.get("token") }],
  };
  const c: ModuleSource = {
    name: "c",
    version: "1.0.0",
    imports: [{ module: "d", version: "1.0.0", names: [{ name: "token" }], loc: "c.ts:1:1" }],
    exports: [{ name: "cToken", kind: "value", signature: "object", make: (ctx) => ctx.get("token") }],
  };
  const a: ModuleSource = {
    name: "a",
    version: "1.0.0",
    imports: [
      { module: "b", version: "1.0.0", names: [{ name: "bToken" }], loc: "a.ts:1:1" },
      { module: "c", version: "1.0.0", names: [{ name: "cToken" }], loc: "a.ts:2:1" },
    ],
    exports: [
      { name: "same", kind: "value", signature: "boolean", make: (ctx) => ctx.get("bToken") === ctx.get("cToken") },
    ],
  };
  const instances = link([d, b, c, a]);
  assert.equal(dEvalCount, 1, "D 的工厂只应执行一次");
  assert.equal(instances.get("a@1.0.0")?.exports.get("same"), true, "菱形两端必须共享同一 D 实例");
});

test("同名遮蔽：本地导出遮蔽同名导入，别名导入仍可用", () => {
  const b: ModuleSource = {
    name: "b",
    version: "1.0.0",
    imports: [],
    exports: [{ name: "util", kind: "decl", signature: "string", decl: "b-util" }],
  };
  const a: ModuleSource = {
    name: "a",
    version: "1.0.0",
    imports: [
      {
        module: "b",
        version: "1.0.0",
        names: [{ name: "util" }, { name: "util", as: "bUtil" }],
        loc: "a.ts:3:1",
      },
    ],
    exports: [
      { name: "util", kind: "decl", signature: "string", decl: "a-util" },
      { name: "check", kind: "value", signature: "string", make: (ctx) => `${ctx.get("util")}/${ctx.get("bUtil")}` },
    ],
  };
  const consumer: ModuleSource = {
    name: "consumer",
    version: "1.0.0",
    imports: [{ module: "a", version: "1.0.0", names: [{ name: "util" }], loc: "consumer.ts:1:1" }],
    exports: [{ name: "seen", kind: "value", signature: "string", make: (ctx) => ctx.get("util") as string }],
  };
  const instances = link([b, a, consumer]);
  assert.equal(instances.get("a@1.0.0")?.exports.get("check"), "a-util/b-util", "本地 util 遮蔽导入，别名 bUtil 指向导入");
  assert.equal(instances.get("consumer@1.0.0")?.exports.get("seen"), "a-util", "外部看到的是 a 的本地导出");
});

test("缺失导出：错误定位到导入语句", () => {
  const b: ModuleSource = {
    name: "b",
    version: "1.0.0",
    imports: [],
    exports: [{ name: "real", kind: "decl", signature: "string", decl: 1 }],
  };
  const a: ModuleSource = {
    name: "a",
    version: "1.0.0",
    imports: [{ module: "b", version: "1.0.0", names: [{ name: "nope" }], loc: "src/a.ts:7:3" }],
    exports: [],
  };
  try {
    link([b, a]);
    assert.fail("应当抛出 LinkFailure");
  } catch (e) {
    const f = e as LinkFailure;
    assert.equal(f.errors[0].code, "MISSING_EXPORT");
    assert.match(f.errors[0].message, /b@1\.0\.0/);
    assert.match(f.errors[0].message, /"nope"/);
    assert.match(f.errors[0].message, /src\/a\.ts:7:3/, "错误必须定位到导入语句");
    assert.equal(f.errors[0].loc, "src/a.ts:7:3");
  }
});

test("版本必须匹配：导入版本与批次版本不一致即失败", () => {
  const b: ModuleSource = {
    name: "b",
    version: "1.0.0",
    imports: [],
    exports: [{ name: "x", kind: "decl", signature: "number", decl: 1 }],
  };
  const a: ModuleSource = {
    name: "a",
    version: "1.0.0",
    imports: [{ module: "b", version: "2.0.0", names: [{ name: "x" }], loc: "a.ts:1:1" }],
    exports: [],
  };
  assert.throws(
    () => link([b, a]),
    (e: unknown) => {
      const f = e as LinkFailure;
      return f.errors[0].code === "VERSION_MISMATCH" && /2\.0\.0/.test(f.errors[0].message) && /1\.0\.0/.test(f.errors[0].message);
    },
  );
});

test("签名检查：期望签名与实际签名不一致即失败，且全部错误一次报出", () => {
  const b: ModuleSource = {
    name: "b",
    version: "1.0.0",
    imports: [],
    exports: [
      { name: "x", kind: "decl", signature: "number", decl: 1 },
      { name: "y", kind: "decl", signature: "string", decl: "s" },
    ],
  };
  const a: ModuleSource = {
    name: "a",
    version: "1.0.0",
    imports: [
      {
        module: "b",
        version: "1.0.0",
        names: [
          { name: "x", signature: "string" },
          { name: "y", signature: "number" },
        ],
        loc: "a.ts:1:1",
      },
    ],
    exports: [],
  };
  try {
    link([b, a]);
    assert.fail("应当抛出 LinkFailure");
  } catch (e) {
    const f = e as LinkFailure;
    assert.equal(f.errors.length, 2, "两个签名错误应一次性全部报出");
    assert.ok(f.errors.every((err) => err.code === "SIGNATURE_MISMATCH"));
  }
});

test("声明环允许：无求值副作用的声明引用可以成环", () => {
  const a: ModuleSource = {
    name: "a",
    version: "1.0.0",
    imports: [{ module: "b", version: "1.0.0", names: [{ name: "schemaB" }], loc: "a.ts:1:1" }],
    exports: [
      { name: "schemaA", kind: "decl", signature: "schema", decl: { refs: ["schemaB"] } },
      { name: "useB", kind: "value", signature: "schema", make: (ctx) => ctx.get("schemaB") },
    ],
  };
  const b: ModuleSource = {
    name: "b",
    version: "1.0.0",
    imports: [{ module: "a", version: "1.0.0", names: [{ name: "schemaA" }], loc: "b.ts:1:1" }],
    exports: [{ name: "schemaB", kind: "decl", signature: "schema", decl: { refs: ["schemaA"] } }],
  };
  const { declGraph, evalGraph } = buildDepGraphs([a, b]);
  assert.notEqual(shortestCycle(declGraph), null, "声明图中应存在环");
  assert.equal(shortestCycle(evalGraph), null, "求值图中不应有环");
  const instances = link([a, b]);
  assert.deepEqual(instances.get("a@1.0.0")?.exports.get("useB"), { refs: ["schemaA"] });
});

test("求值环禁止：输出最短错误来源链", () => {
  const mk = (name: string, deps: string[]): ModuleSource => ({
    name,
    version: "1.0.0",
    imports: deps.map((d) => ({ module: d, version: "1.0.0", names: [{ name: "v" }], loc: `${name}.ts:1:1` })),
    exports: [{ name: "v", kind: "value", signature: "number", make: () => 1 }],
  });
  // 短环 a<->b（2 条边）与长环 a->c->d->a（3 条边）同时存在
  const a = mk("a", ["b", "c"]);
  const b = mk("b", ["a"]);
  const c = mk("c", ["d"]);
  const d = mk("d", ["a"]);
  try {
    link([a, b, c, d]);
    assert.fail("应当抛出 LinkFailure");
  } catch (e) {
    const f = e as LinkFailure;
    assert.equal(f.errors[0].code, "EVAL_CYCLE");
    assert.deepEqual(f.errors[0].chain, ["a@1.0.0", "b@1.0.0", "a@1.0.0"], "必须输出最短来源链");
    assert.match(f.errors[0].message, /a@1\.0\.0 -> b@1\.0\.0 -> a@1\.0\.0/);
  }
});
