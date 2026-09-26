import {
  ArrayStreamValidator,
  StreamAbortedError,
} from "../src/stream.ts";
import type { StreamSchema } from "../src/schema.ts";

const schema: StreamSchema = {
  items: {
    type: "object",
    properties: {
      id: { type: "integer", minimum: 0 },
      sku: { type: "string", pattern: "^[A-Z]{3}-\\d+$" },
      price: { type: "number", minimum: 0 },
    },
    required: ["id", "sku", "price"],
    additionalProperties: false,
  },
  minItems: 100_000,
  uniqueItems: true,
  uniqueKeys: ["sku"],
  aggregates: [{ kind: "sum", field: "price", max: 10_000_000 }],
};

function* generateOrders(count: number) {
  for (let i = 0; i < count; i++) {
    yield { id: i, sku: `SKU-${i}`, price: 9.99 };
  }
}

// --- Scenario 1: 100k items, the last one duplicates the first -------------
console.log("== scenario 1: 100k items, item #99999 duplicates item #0 ==");
const v1 = new ArrayStreamValidator(schema, "collect");
const t0 = performance.now();
let pushed = 0;
for (const order of generateOrders(100_000)) {
  const item = pushed === 99_999 ? { id: 0, sku: "SKU-0", price: 9.99 } : order;
  const errs = v1.push(item);
  if (errs.length > 0) {
    for (const e of errs) {
      console.log(`  error at instance ${e.instancePath} (rule ${e.schemaPath}): ${e.message}`);
    }
  }
  pushed++;
}
const result1 = v1.finish();
console.log(
  `  pushed ${result1.itemCount} items in ${(performance.now() - t0).toFixed(0)}ms, valid=${result1.valid}, errors=${result1.errors.length}`,
);

// --- Scenario 2: fail-fast stops at the first bad item ---------------------
console.log("== scenario 2: fail-fast mode ==");
const v2 = new ArrayStreamValidator(schema, "fail-fast");
try {
  v2.push({ id: 0, sku: "SKU-0", price: 1 });
  v2.push({ id: -1, sku: "bad", price: 2 });
  console.log("  unreachable");
} catch (e) {
  const err = e as StreamAbortedError;
  for (const ve of err.errors) {
    console.log(`  aborted at instance ${ve.instancePath} (rule ${ve.schemaPath}): ${ve.message}`);
  }
}

// --- Scenario 3: unfinished stream never reports overall pass --------------
console.log("== scenario 3: verdict before finish ==");
const v3 = new ArrayStreamValidator(schema);
v3.push({ id: 0, sku: "SKU-0", price: 1 });
console.log(`  verdict after 1 push: ${v3.verdict}`); // pending, not valid
const result3 = v3.finish();
console.log(`  after finish: valid=${result3.valid}`);
for (const e of result3.errors) {
  console.log(`  error at instance "${e.instancePath}" (rule ${e.schemaPath}): ${e.message}`);
}
