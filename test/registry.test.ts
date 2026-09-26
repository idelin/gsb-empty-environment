import { test } from "node:test";
import assert from "node:assert/strict";
import { SchemaRegistry, SchemaConflictError } from "../src/registry.ts";
import type { Schema } from "../src/schema.ts";

const v1: Schema = {
  type: "object",
  required: ["version", "fullname"],
  properties: {
    version: { const: "v1" },
    fullname: { type: "string", minLength: 1 },
  },
  additionalProperties: false,
};

const v2: Schema = {
  type: "object",
  required: ["version", "firstName", "lastName"],
  properties: {
    version: { const: "v2" },
    firstName: { type: "string", minLength: 1 },
    lastName: { type: "string", minLength: 1 },
  },
  additionalProperties: false,
};

const v3: Schema = {
  type: "object",
  required: ["version", "firstName", "lastName", "age"],
  properties: {
    version: { const: "v3" },
    firstName: { type: "string", minLength: 1 },
    lastName: { type: "string", minLength: 1 },
    age: { type: "integer", minimum: 0 },
  },
  additionalProperties: false,
};

function buildRegistry(): SchemaRegistry {
  const reg = new SchemaRegistry();
  reg.registerSchema("v1", v1);
  reg.registerSchema("v2", v2);
  reg.registerSchema("v3", v3);
  reg.registerMigration("v1", "v2", (doc) => {
    const d = doc as Record<string, unknown>;
    const [firstName, ...rest] = String(d.fullname).split(" ");
    return { version: "v2", firstName, lastName: rest.join(" ") || "-" };
  });
  reg.registerMigration("v2", "v3", (doc) => ({ ...(doc as object), version: "v3", age: 0 }));
  return reg;
}

test("同版本号不同内容 → 摘要冲突", () => {
  const reg = new SchemaRegistry();
  reg.registerSchema("v1", v1);
  assert.throws(() => reg.registerSchema("v1", { ...v1, required: ["version"] }), SchemaConflictError);
  // 同内容（键序不同也算同内容）→ 幂等通过
  const again = reg.registerSchema("v1", {
    additionalProperties: false,
    properties: {
      fullname: { minLength: 1, type: "string" },
      version: { const: "v1" },
    },
    required: ["version", "fullname"],
    type: "object",
  } as Schema);
  assert.equal(again, reg.digestOf("v1"));
});

test("未知版本拒绝", () => {
  const reg = buildRegistry();
  const res = reg.load({ version: "v9", fullname: "Ada Lovelace" });
  assert.equal(res.ok, false);
  if (!res.ok) {
    assert.equal(res.stage, "version");
    assert.match(res.errors[0] as string, /unknown version "v9"/);
  }
});

test("缺版本默认拒绝；显式 defaultVersion 才放行", () => {
  const reg = buildRegistry();
  const denied = reg.load({ fullname: "Ada Lovelace" });
  assert.equal(denied.ok, false);
  if (!denied.ok) assert.equal(denied.stage, "version");

  const allowed = reg.load({ fullname: "Ada Lovelace" }, { defaultVersion: "v1" });
  assert.equal(allowed.ok, true);
  if (allowed.ok) {
    assert.equal(allowed.sourceVersion, "v1");
    assert.equal(allowed.targetVersion, "v3");
  }
});

test("v1 文档沿显式迁移链 v1→v2→v3 并再验证", () => {
  const reg = buildRegistry();
  const res = reg.load({ version: "v1", fullname: "Ada Lovelace" });
  assert.equal(res.ok, true);
  if (res.ok) {
    assert.deepEqual(res.appliedMigrations, ["v1->v2", "v2->v3"]);
    assert.deepEqual(res.document, {
      version: "v3",
      firstName: "Ada",
      lastName: "Lovelace",
      age: 0,
    });
  }
});

test("迁移抛错：分阶段报告且原文保留", () => {
  const reg = buildRegistry();
  reg.registerSchema("v1b", v1);
  reg.registerMigration("v1b", "v2", () => {
    throw new Error("corrupt payload");
  });
  const original = { version: "v1b", fullname: "Ada Lovelace" };
  const snapshot = structuredClone(original);
  const res = reg.load(original, { targetVersion: "v2" });
  assert.equal(res.ok, false);
  if (!res.ok) {
    assert.equal(res.stage, "migration");
    if (res.stage === "migration") {
      assert.match(res.errors[0].message, /corrupt payload/);
      assert.deepEqual(res.originalDocument, snapshot); // 原文随结果返回
    }
  }
  assert.deepEqual(original, snapshot); // 入参未被改动
});

test("迁移产物不合目标 schema：报 migration 阶段并带验证细节", () => {
  const reg = new SchemaRegistry();
  reg.registerSchema("v1", v1);
  reg.registerSchema("v2", v2);
  reg.registerMigration("v1", "v2", (doc) => ({
    version: "v2",
    firstName: (doc as Record<string, unknown>).fullname, // 漏了 lastName
  }));
  const res = reg.load({ version: "v1", fullname: "Ada" }, { targetVersion: "v2" });
  assert.equal(res.ok, false);
  if (!res.ok && res.stage === "migration") {
    assert.equal(res.errors[0].step, "v1->v2");
    assert.equal(res.errors[0].validationErrors?.[0].keyword, "required");
    assert.equal(res.errors[0].validationErrors?.[0].schemaPath, "/required");
  } else {
    assert.fail("expected migration-stage failure");
  }
});

test("无迁移链可达目标版本：拒绝而非隐式猜测", () => {
  const reg = new SchemaRegistry();
  reg.registerSchema("v1", v1);
  reg.registerSchema("v3", v3);
  const res = reg.load({ version: "v1", fullname: "Ada" }, { targetVersion: "v3" });
  assert.equal(res.ok, false);
  if (!res.ok) assert.equal(res.stage, "migration");
});

test("当前版本数据不合自身 schema：validation 阶段，错误含双路径", () => {
  const reg = buildRegistry();
  const res = reg.load({ version: "v3", firstName: "", lastName: "L", age: -5 });
  assert.equal(res.ok, false);
  if (!res.ok && res.stage === "validation") {
    const byKeyword = Object.fromEntries(res.errors.map((e) => [e.keyword, e]));
    assert.equal(byKeyword.minLength.instancePath, "/firstName");
    assert.equal(byKeyword.minLength.schemaPath, "/properties/firstName/minLength");
    assert.equal(byKeyword.minimum.instancePath, "/age");
    assert.equal(byKeyword.minimum.schemaPath, "/properties/age/minimum");
  } else {
    assert.fail("expected validation-stage failure");
  }
});

test("targetVersion 指定后只迁移到该版本", () => {
  const reg = buildRegistry();
  const res = reg.load({ version: "v1", fullname: "Ada Lovelace" }, { targetVersion: "v2" });
  assert.equal(res.ok, true);
  if (res.ok) {
    assert.equal(res.targetVersion, "v2");
    assert.deepEqual(res.appliedMigrations, ["v1->v2"]);
  }
});
