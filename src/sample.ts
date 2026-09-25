import { and, cmp, inSet, lit, not, or, resetIds, show, type Expr } from "./ast.ts";
import { evalExpr } from "./eval.ts";
import { checkEquivalence } from "./equiv.ts";
import { evaluatePolicy, type Policy } from "./policy.ts";
import { simplify, type Domains } from "./simplify.ts";

function section(title: string): void {
  console.log(`\n=== ${title} ===`);
}

function showSourceMap(e: Expr, indent = "  "): void {
  const label =
    e.kind === "cond" || e.kind === "const" ? show(e) : e.kind.toUpperCase();
  console.log(`${indent}#${e.id} ${label}  <=  sources [${e.from.join(", ")}]`);
  if (e.kind === "not") showSourceMap(e.child, indent + "  ");
  if (e.kind === "and" || e.kind === "or") {
    for (const a of e.args) showSourceMap(a, indent + "  ");
  }
}

resetIds();

const policy: Policy = {
  name: "employee-access",
  attributes: [
    { name: "age", min: 0, max: 120, required: true },
    { name: "dept", min: 100, max: 110, required: true },
    { name: "level", min: 1, max: 5, required: false }, // may be missing
  ],
  expr: or(
    and(cmp("age", ">=", 18), cmp("age", "<=", 65), inSet("dept", [101, 102])),
    and(cmp("level", ">=", 4), cmp("level", "<=", 5)),
    and(lit(false), cmp("age", ">", 100)),
  ),
};

const domains: Domains = Object.fromEntries(
  policy.attributes.map((a) => [a.name, { min: a.min, max: a.max, required: a.required }]),
);

section("1. Original expression");
console.log(show(policy.expr));

section("2. Simplified expression (three-valued semantics preserved)");
const simplified = simplify(policy.expr, domains);
console.log(show(simplified));
console.log("source map:");
showSourceMap(simplified);

section("3. Equivalence over the full finite domain");
const eq = checkEquivalence(policy.attributes, policy.expr, simplified);
console.log(`checked ${eq.checked} inputs, equivalent: ${eq.equivalent}`);

section("4. Excluded middle is NOT folded when the attribute may be missing");
const x = cmp("level", ">=", 3);
const excludedMiddle = or(x, not(x));
const emSimplified = simplify(excludedMiddle, domains);
console.log(`original:   ${show(excludedMiddle)}`);
console.log(`simplified: ${show(emSimplified)}   (still not TRUE)`);
console.log(`level missing -> ${evalExpr(emSimplified, { level: undefined })}`);
const naive = checkEquivalence(
  policy.attributes.filter((a) => a.name === "level"),
  excludedMiddle,
  lit(true),
);
const showEnv = (env: Record<string, number | undefined>): string =>
  `{${Object.entries(env)
    .map(([k, v]) => `${k}: ${v === undefined ? "missing" : v}`)
    .join(", ")}}`;
const m = naive.mismatches[0];
console.log(
  `naive fold to TRUE equivalent: ${naive.equivalent}, first mismatch: env ${showEnv(m.env)} -> original ${m.a} vs folded ${m.b}`,
);
const requiredEm = or(cmp("age", ">=", 18), not(cmp("age", ">=", 18)));
console.log(`required attribute age: ${show(requiredEm)} -> ${show(simplify(requiredEm, domains))}`);

section("5. Policy evaluation reads only declared attributes");
const context = {
  age: 30,
  dept: 102,
  level: 2,
  ssn: "should-never-be-read",
  role: "external-service-field",
};
console.log(`context ${JSON.stringify(context)} -> ${evaluatePolicy(policy, context)}`);
console.log(`level missing -> ${evaluatePolicy(policy, { age: 30, dept: 102 })}`);
console.log(`age 70, level 5 -> ${evaluatePolicy(policy, { age: 70, dept: 105, level: 5 })}`);
