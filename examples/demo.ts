/**
 * Runnable sample: node examples/demo.ts  (Node >= 22.18, no dependencies)
 */
import { parseDecimal, toScientificString, DecimalBudgetError } from "../src/decimal.ts";
import { contains, validate } from "../src/validate.ts";
import { intersect } from "../src/intersect.ts";
import { counterexample } from "../src/counterexample.ts";

const show = (title: string, value: unknown): void => {
  console.log(`\n=== ${title} ===`);
  console.log(typeof value === "string" ? value : JSON.stringify(value, null, 2));
};

// 1. Exact multiples: IEEE-754 says 0.3 / 0.1 === 2.9999999999999996; we say 3.
show("0.3 is an integer multiple of 0.1 (binary floats disagree)", {
  floatQuotient: 0.3 / 0.1,
  contains: contains({ multipleOf: "0.1" }, "0.3"),
});

// 2. Digit counts are numeric, not textual: 1e3 is "1000" -> 4 digits.
show("1e3 has 4 total digits by value, not 3 by text", {
  "maxTotalDigits: 3": validate("1e3", { maxTotalDigits: 3 }),
  "maxTotalDigits: 4": validate("1e3", { maxTotalDigits: 4 }).valid,
});

// 3. Negative zero equals positive zero; the original literal is preserved.
const negZero = parseDecimal("-0");
show("-0 equals 0 numerically, literal preserved", {
  "contains({ minimum: '0' }, '-0')": contains({ minimum: "0" }, "-0"),
  preservedText: negZero.text,
  canonical: toScientificString(negZero),
});

// 4. Errors carry both the instance path and the rule path.
show("errors give instancePath and rulePath", validate("12.345", {
  maximum: "10",
  maxFractionDigits: 2,
}, { instancePath: "/items/0/price" }).errors);

// 5. Feasibility intersection: witness or precise no-solution evidence.
show("intersect: feasible witness", intersect({
  minimum: "0.15",
  maximum: "0.25",
  multipleOf: "0.1",
}));
show("intersect: exact no-solution evidence", intersect({
  minimum: "0.15",
  maximum: "0.25",
  multipleOf: "0.5",
}));
show("intersect: digit cap makes it infeasible", intersect({
  minimum: "1000",
  maxTotalDigits: 3,
}));

// 6. Minimal destructive counterexamples.
show("minimal counterexample for { minimum: '5' }", counterexample({ minimum: "5" }));
show("minimal counterexample for { multipleOf: '0.5' }", counterexample({ multipleOf: "0.5" }));
show("no counterexample for an unconstrained schema", counterexample({}));

// 7. Budget control: huge exponents are rejected before any allocation.
const started = Date.now();
try {
  parseDecimal("1e999999999999999999");
} catch (err) {
  show("1e999999999999999999 rejected by budget, no zero-run allocated", {
    error: err instanceof DecimalBudgetError ? err.message : String(err),
    elapsedMs: Date.now() - started,
  });
}

// 8. Scientific exponents and negatives never touch floats.
show("scientific bounds without floats", {
  "contains({ minimum: '-2.5e-4', maximum: '2.5e-4' }, '-0.00025')": contains(
    { minimum: "-2.5e-4", maximum: "2.5e-4" },
    "-0.00025",
  ),
  "intersect witness for 1e-3..5e-3 step 2e-3": intersect({
    minimum: "1e-3",
    maximum: "5e-3",
    multipleOf: "2e-3",
  }),
});
