import { SchemaRegistry, SchemaConflictError } from "../src/index.ts";
import type { Schema } from "../src/index.ts";

// ── 三个版本的 schema（只承诺实现的关键学子集） ──
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
  required: ["version", "firstName", "lastName", "email"],
  properties: {
    version: { const: "v3" },
    firstName: { type: "string", minLength: 1 },
    lastName: { type: "string", minLength: 1 },
    email: { type: "string", pattern: "^[^@]+@[^@]+$" },
    age: { type: "integer", minimum: 0 },
  },
  additionalProperties: false,
};

const registry = new SchemaRegistry();
for (const [v, s] of [["v1", v1], ["v2", v2], ["v3", v3]] as const) {
  console.log(`register ${v}  digest=${registry.registerSchema(v, s).slice(0, 12)}…`);
}
registry.registerMigration("v1", "v2", (doc) => {
  const d = doc as Record<string, unknown>;
  const [firstName, ...rest] = String(d.fullname).split(" ");
  return { version: "v2", firstName, lastName: rest.join(" ") || "-" };
});
registry.registerMigration("v2", "v3", (doc) => ({
  ...(doc as Record<string, unknown>),
  version: "v3",
  email: "unknown@example.com",
}));

const show = (label: string, result: unknown): void => {
  console.log(`\n### ${label}`);
  console.log(JSON.stringify(result, null, 2));
};

// 1. 旧版本文档 → 显式迁移链 → 再验证
show("v1 文档迁移到 v3", registry.load({ version: "v1", fullname: "Ada Lovelace" }));

// 2. 未知版本拒绝
show("未知版本 v9", registry.load({ version: "v9", fullname: "Ada Lovelace" }));

// 3. 缺版本：默认拒绝；显式 defaultVersion 才放行
show("缺版本（无默认）", registry.load({ fullname: "Ada Lovelace" }));
show("缺版本（显式 defaultVersion=v1）",
  registry.load({ fullname: "Ada Lovelace" }, { defaultVersion: "v1" }));

// 4. 验证错误：实例路径 + 规则路径
show("v3 数据不合规",
  registry.load({ version: "v3", firstName: "", lastName: "L", email: "not-an-email", age: -3 }));

// 5. 迁移失败保留原文
const broken = new SchemaRegistry();
broken.registerSchema("v1", v1);
broken.registerSchema("v2", v2);
broken.registerMigration("v1", "v2", () => {
  throw new Error("disk read failed");
});
const original = { version: "v1", fullname: "Ada Lovelace" };
show("迁移抛错（原文保留）", broken.load(original, { targetVersion: "v2" }));
console.log("原对象未被改动:", JSON.stringify(original));

// 6. 同版本号不同摘要 → 冲突
try {
  registry.registerSchema("v1", { ...v1, required: ["version"] });
} catch (e) {
  console.log("\n### 同版本号不同摘要");
  console.log(e instanceof SchemaConflictError ? `${e.name}: ${e.message}` : String(e));
}
