import { compareContracts, type JsonSchema } from "../src/index.ts";

const cases: Array<[string, JsonSchema, JsonSchema]> = [
  [
    "1. 收紧最大值 maximum 100 -> 50（破坏）",
    { type: "integer", minimum: 0, maximum: 100 },
    { type: "integer", minimum: 0, maximum: 50 },
  ],
  [
    "2. 放宽最大值 maximum 50 -> 100（兼容）",
    { type: "integer", minimum: 0, maximum: 50 },
    { type: "integer", minimum: 0, maximum: 100 },
  ],
  [
    "3. 枚举缩小 [a,b,c] -> [a,b]（破坏）",
    { enum: ["a", "b", "c"] },
    { enum: ["a", "b"] },
  ],
  [
    "4. 新增可选字段，旧契约 additionalProperties:false（兼容）",
    {
      type: "object",
      properties: { name: { type: "string" } },
      required: ["name"],
      additionalProperties: false,
    },
    {
      type: "object",
      properties: { name: { type: "string" }, age: { type: "integer", minimum: 0 } },
      required: ["name"],
      additionalProperties: false,
    },
  ],
  [
    "5. 新增可选字段，旧契约允许任意额外字段（破坏）",
    { type: "object", properties: { name: { type: "string" } }, required: ["name"] },
    {
      type: "object",
      properties: { name: { type: "string" }, age: { type: "integer", minimum: 0 } },
      required: ["name"],
    },
  ],
  [
    "6. 新增必填字段（破坏）",
    { type: "object", properties: { id: { type: "integer" } }, additionalProperties: false },
    {
      type: "object",
      properties: { id: { type: "integer" } },
      required: ["id"],
      additionalProperties: false,
    },
  ],
  [
    "7. 数组元素最大值收紧（破坏，报告实例路径与规则路径）",
    { type: "array", items: { type: "integer", maximum: 10 } },
    { type: "array", items: { type: "integer", maximum: 5 } },
  ],
  [
    "8. 新契约使用未支持关键字 pattern（无法证明 -> unknown）",
    { type: "string", minLength: 1 },
    { type: "string", minLength: 1, pattern: "^[a-z]+$" },
  ],
];

for (const [name, oldSchema, newSchema] of cases) {
  console.log("=".repeat(72));
  console.log(name);
  console.log("old:", JSON.stringify(oldSchema));
  console.log("new:", JSON.stringify(newSchema));
  console.log(JSON.stringify(compareContracts(oldSchema, newSchema), null, 2));
}
