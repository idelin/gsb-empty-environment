import { IncrementalValidator, validate, type PatchResult } from "../src/validator.ts";
import type { Json, SchemaNode, ValidationError } from "../src/checks.ts";

const schema: SchemaNode = {
  type: "object",
  properties: {
    x: { type: "number" },
    y: { type: "number" },
    z: { type: "string", minLength: 3 },
    profile: {
      type: "object",
      properties: {
        age: { type: "integer", minimum: 0 },
        tags: { type: "array", items: { type: "string" } },
      },
      required: ["age"],
    },
  },
  required: ["x", "y"],
  rules: [{ kind: "lt", left: "/x", right: "/y" }],
};

const data: Json = {
  x: 1,
  y: 2,
  z: "ok",
  profile: { age: -3, tags: ["a", 7] },
};

function showErrors(title: string, errors: ValidationError[]): void {
  console.log(title);
  if (errors.length === 0) {
    console.log("  (无)");
  }
  for (const e of errors) {
    console.log(`  实例路径 ${e.instancePath || "(root)"}  |  规则路径 ${e.schemaPath}  |  ${e.message}`);
  }
}

function showDiff(result: PatchResult): void {
  showErrors("  新增错误:", result.added);
  showErrors("  撤销错误:", result.removed);
  console.log(`  本轮重验的检查项: ${result.revalidated.join(", ")}`);
}

const engine = new IncrementalValidator(schema, data);

console.log("== 1. 初始全量验证 ==");
showErrors("当前错误集:", engine.getErrors());

console.log("\n== 2. 局部修改 /x = 5（违反规则 x < y）==");
const r1 = engine.apply([{ op: "set", path: "/x", value: 5 }]);
showDiff(r1);
console.log(`  观察: 与 /z 相关的检查项未被重验 -> ${r1.revalidated.every((p) => !p.startsWith("#/properties/z"))}`);

console.log("\n== 3. 删除父节点 /profile（子树错误应整体撤销）==");
const r2 = engine.apply([{ op: "remove", path: "/profile" }]);
showDiff(r2);

console.log("\n== 4. 修复 /x = 1（规则错误应撤销）==");
const r3 = engine.apply([{ op: "set", path: "/x", value: 1 }]);
showDiff(r3);

console.log("\n== 5. 一致性: 增量错误集 vs 全量重算 ==");
const incremental = engine.getErrors();
const full = validate(schema, engine.getData());
const same = JSON.stringify(incremental) === JSON.stringify(full);
console.log(`  增量 ${incremental.length} 条, 全量 ${full.length} 条, 一致 -> ${same}`);
if (!same) process.exitCode = 1;
