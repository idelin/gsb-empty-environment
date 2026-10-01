import { ErrorIndex, measureSpaceSavings } from "../src/error-index.ts";
import { pathToString, toMultiset, type ValidationError } from "../src/errors.ts";
import { validate, type Schema } from "../src/validator.ts";

const schema: Schema = {
  type: "object",
  properties: {
    items: {
      type: "array",
      items: {
        type: "object",
        properties: {
          name: { type: "string", minLength: 1 },
          qty: { type: "integer", minimum: 1 },
        },
        required: ["name", "qty"],
        additionalProperties: false,
      },
    },
  },
  required: ["items"],
};

function section(title: string): void {
  console.log(`\n=== ${title} ===`);
}

// ---- Scenario 1: items 1..100 all fail the same rule -----------------------
section("Scenario 1: 100 consecutive failures of the same rule");
const data1 = {
  items: Array.from({ length: 100 }, (_, i) => ({ name: `row-${i + 1}`, qty: 0 })),
};
const errors1 = validate(schema, data1, { schemaVersion: "orders/v1" });
console.log(`raw errors: ${errors1.length}`);
console.log(`first error: ${JSON.stringify({
  ...errors1[0],
  instancePath: pathToString(errors1[0].instancePath),
})}`);

const index1 = ErrorIndex.fromErrors(errors1);
const serialized1 = index1.serialize();
console.log(`groups: ${index1.groupCount}, totalCount: ${index1.totalCount}`);
console.log(`compressed ranges: ${JSON.stringify(serialized1.groups[0].ranges)}`);

const same =
  JSON.stringify([...toMultiset(index1.expand())].sort()) ===
  JSON.stringify([...toMultiset(errors1)].sort());
console.log(`decompress == original error set: ${same}`);

// ---- Scenario 2: one missing item in the middle ----------------------------
section("Scenario 2: item 50 is fixed -> range must split, never fake-continuous");
const errors2 = errors1.filter((e) => e.instancePath[1] !== 49); // 0-based: item #50
const index2 = ErrorIndex.fromErrors(errors2);
console.log(`groups: ${index2.groupCount}, totalCount: ${index2.totalCount}`);
console.log(`compressed ranges: ${JSON.stringify(index2.serialize().groups[0].ranges)}`);
const same2 =
  JSON.stringify([...toMultiset(index2.expand())].sort()) ===
  JSON.stringify([...toMultiset(errors2)].sort());
console.log(`decompress == original error set: ${same2}`);

// ---- Scenario 3: online add / retract without rebuilding -------------------
section("Scenario 3: online add & retract");
const index3 = ErrorIndex.fromErrors(errors1);
const fixedRow = errors1.find((e) => e.instancePath[1] === 7) as ValidationError;
console.log(`retract /items/7/qty -> ${index3.retractError(fixedRow)}`);
console.log(`totalCount after retract: ${index3.totalCount}`);
console.log(`ranges now: ${JSON.stringify(index3.serialize().groups[0].ranges)}`);
const newError: ValidationError = {
  schemaVersion: "orders/v1",
  rule: "minimum",
  instancePath: ["items", 7, "qty"],
  schemaPath: "#/properties/items/items/properties/qty/minimum",
  message: "must be >= 1",
  params: { minimum: 1 },
};
index3.addError(newError);
console.log(`re-added /items/7/qty -> totalCount: ${index3.totalCount}`);
console.log(`ranges now: ${JSON.stringify(index3.serialize().groups[0].ranges)}`);

// ---- Scenario 4: random access expansion -----------------------------------
section("Scenario 4: expand by rule / path prefix / original order");
const mixed = validate(
  schema,
  {
    items: [
      { name: "", qty: 0, extra: true },
      { qty: 0 },
      { name: "ok", qty: 5 },
    ],
  },
  { schemaVersion: "orders/v1" },
);
const mixedIndex = ErrorIndex.fromErrors(mixed);
console.log(`mixed errors: ${mixedIndex.totalCount} in ${mixedIndex.groupCount} groups`);
for (const rule of ["minimum", "required", "additionalProperties", "minLength"]) {
  console.log(`  rule=${rule}: ${mixedIndex.count({ rule })}`);
}
console.log(
  `  prefix=/items/0: ${mixedIndex.count({ pathPrefix: "/items/0" })} error(s)`,
);
console.log("  original order expansion:");
for (const e of mixedIndex.expand({ order: "original" })) {
  console.log(`    [${e.rule}] ${pathToString(e.instancePath)}  (${e.schemaPath})`);
}

// ---- Scenario 5: space savings ---------------------------------------------
section("Scenario 5: measured space savings (scenario 1)");
const savings = measureSpaceSavings(errors1, index1);
console.log(`raw error list:        ${savings.rawBytes} bytes`);
console.log(`compressed index:      ${savings.compressedBytes} bytes`);
console.log(
  `saved:                 ${savings.savedBytes} bytes (${((1 - savings.ratio) * 100).toFixed(1)}% smaller, ratio ${savings.ratio.toFixed(3)})`,
);

// ---- Scenario 6: tamper detection ------------------------------------------
section("Scenario 6: tamper detection on the serialized form");
const forged = JSON.parse(JSON.stringify(serialized1));
forged.totalCount = 3;
try {
  ErrorIndex.deserialize(forged);
  console.log("forged payload accepted (BUG!)");
} catch (err) {
  console.log(`forged totalCount rejected: ${(err as Error).message}`);
}
