import { applyDefaultsAndValidate, type Schema } from "../src/index.ts";

const orderSchema: Schema = {
  type: "object",
  properties: {
    customer: {
      type: "object",
      properties: {
        firstName: { type: "string" },
        lastName: { type: "string" },
        displayName: {
          type: "string",
          default: { $expr: { op: "concat", args: [{ $get: "firstName" }, " ", { $get: "lastName" }] } },
        },
      },
      required: ["firstName", "lastName"],
    },
    currency: { type: "string", default: "CNY", enum: ["CNY", "USD"] },
    lines: {
      type: "array",
      minItems: 1,
      items: {
        type: "object",
        properties: {
          sku: { type: "string", pattern: "^[A-Z0-9-]+$" },
          qty: { type: "integer", minimum: 1, default: 1 },
          price: { type: "number", exclusiveMinimum: 0 },
          amount: {
            type: "number",
            default: { $expr: { op: "mul", args: [{ $get: "qty" }, { $get: "price" }] } },
          },
          meta: { type: "object", default: { discounts: [] } },
        },
        required: ["sku", "price"],
      },
    },
    note: { default: "no note" }, // explicit null stays null
  },
  required: ["customer", "lines"],
};

const show = (title: string, result: ReturnType<typeof applyDefaultsAndValidate>): void => {
  console.log(`\n=== ${title} ===`);
  if (result.ok) {
    console.log("OK:", JSON.stringify(result.value, null, 2));
  } else {
    console.log("FAILED, rolled back. Issues:");
    for (const e of result.errors) {
      console.log(`  [${e.keyword}] instance "${e.instancePath}" schema "${e.schemaPath}": ${e.message}`);
    }
  }
};

// 1. Happy path: defaults + topological expression defaults + per-element isolation
const okInput = {
  customer: { firstName: "Ada", lastName: "Lovelace" },
  lines: [
    { sku: "BOOK-1", price: 40, qty: 2 },
    { sku: "PEN-9", price: 3.5 },
  ],
  note: null,
};
const okResult = applyDefaultsAndValidate(orderSchema, okInput);
show("valid order (defaults filled)", okResult);
if (okResult.ok) {
  const lines = (okResult.value as typeof okInput & { lines: { meta: { discounts: string[] } }[] }).lines;
  lines[0].meta.discounts.push("VIP");
  console.log("element isolation check:", JSON.stringify(lines.map((l) => l.meta)));
}
console.log("input untouched:", JSON.stringify(okInput) === JSON.stringify({
  customer: { firstName: "Ada", lastName: "Lovelace" },
  lines: [{ sku: "BOOK-1", price: 40, qty: 2 }, { sku: "PEN-9", price: 3.5 }],
  note: null,
}));

// 2. Validation failure -> full rollback, errors with both paths
const badInput = {
  customer: { firstName: "Ada", lastName: "Lovelace" },
  lines: [{ sku: "bad sku!", price: -1 }],
};
show("invalid order (rollback)", applyDefaultsAndValidate(orderSchema, badInput));
console.log("bad input still:", JSON.stringify(badInput));

// 3. Default dependency cycle -> chain reported
const cyclicSchema: Schema = {
  type: "object",
  properties: {
    x: { default: { $expr: { op: "concat", args: [{ $get: "y" }, "x"] } } },
    y: { default: { $expr: { op: "concat", args: [{ $get: "z" }, "y"] } } },
    z: { default: { $expr: { op: "concat", args: [{ $get: "x" }, "z"] } } },
  },
};
show("cyclic defaults", applyDefaultsAndValidate(cyclicSchema, {}));
