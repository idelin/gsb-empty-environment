/**
 * Runnable tour of the library: node examples/demo.ts
 */
import {
  canonical,
  compareDecimal,
  contains,
  isMultipleOf,
  parseDecimal,
  precisionOf,
  solve,
  validate,
  normalizeConstraints,
} from "../src/index.ts";

function show(title: string, fn: () => void): void {
  console.log(`\n=== ${title} ===`);
  fn();
}

show("0.3 is an integer multiple of 0.1 (exact, no binary float)", () => {
  console.log("isMultipleOf(0.3, 0.1) =", isMultipleOf(parseDecimal("0.3"), parseDecimal("0.1")));
  console.log("for contrast, IEEE754: 0.3 / 0.1 =", 0.3 / 0.1);
});

show("1e3 digit count follows the numeric value, not the literal", () => {
  const d = parseDecimal("1e3");
  console.log(`canonical=${canonical(d)} precision=${precisionOf(d)} (literal "1e3" is 3 chars)`);
});

show("negative zero equals positive zero; raw text preserved", () => {
  const nz = parseDecimal("-0");
  console.log("compare(-0, 0) =", compareDecimal(nz, parseDecimal("0")), "| raw =", JSON.stringify(nz.raw));
});

show("scientific notation normalizes by definition", () => {
  for (const s of ["1.2300e2", "12.5e-1", "1E-3"]) {
    console.log(`${s} -> ${canonical(parseDecimal(s))}`);
  }
});

show("constraint intersection: feasible witness", () => {
  const r = solve(normalizeConstraints({ minimum: "0.05", maximum: "0.2", multipleOf: "0.1", scale: 2 }));
  console.log("[0.05,0.2] ∩ 0.1ℤ ∩ scale≤2 ->", r.feasible ? `witness ${r.canonical}` : r.evidence);
});

show("constraint intersection: precise no-solution evidence", () => {
  const r = solve(normalizeConstraints({ minimum: "0.15", maximum: "0.25", multipleOf: "1" }));
  console.log("[0.15,0.25] ∩ 1ℤ ->", r.feasible ? `witness ${r.canonical}` : r.evidence);
});

show("instance validation: instance path + schema path", () => {
  const r = validate(
    { price: "0.30000000000000004" },
    { properties: { price: { precision: 3, multipleOf: "0.1" } } },
  );
  for (const e of r.errors) {
    console.log(`instancePath=${e.instancePath} schemaPath=${e.schemaPath}: ${e.message}`);
  }
});

show("contract containment with minimal counterexample", () => {
  const r = contains({ multipleOf: "0.1" }, { multipleOf: "0.01" });
  console.log("contained =", r.contained, "| counterexample =", r.counterexample?.canonical);
  const ok = contains(
    { multipleOf: "0.5", minimum: "-10", maximum: "10" },
    { multipleOf: "1", minimum: "-5", maximum: "5" },
  );
  console.log("contained =", ok.contained, "|", ok.reason);
});

show("huge exponents are budget-rejected before allocation", () => {
  try {
    parseDecimal("1e999999999999999999");
  } catch (err) {
    console.log((err as Error).name + ":", (err as Error).message);
  }
});
