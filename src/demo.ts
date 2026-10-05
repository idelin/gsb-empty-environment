import { TermFactory, canon, v, p } from "./term.ts";
import { Engine } from "./engine.ts";
import type { Rule } from "./engine.ts";

const fac = new TermFactory();
const c = (op: string) => fac.mk(op);

const rules: Rule[] = [
  { name: "add-zero", lhs: p("add", p("zero"), v("x")), rhs: v("x") },
  { name: "mul-one", lhs: p("mul", p("one"), v("x")), rhs: v("x") },
  { name: "mul-zero", lhs: p("mul", p("zero"), v("x")), rhs: p("zero") },
  { name: "neg-neg", lhs: p("neg", p("neg", v("x"))), rhs: v("x") },
];

const initial = fac.mk("neg", [
  fac.mk("neg", [fac.mk("add", [c("zero"), fac.mk("mul", [c("one"), c("a")])])]),
]);

const engine = new Engine(rules, initial, fac);
console.log(`initial: ${canon(initial)}`);
const res = engine.run();
for (const s of engine.steps) {
  const at = s.path.length === 0 ? "root" : s.path.join(".");
  const binds = s.bindings.map(([k, t]) => `${k} ↦ ${canon(t)}`).join(", ");
  console.log(
    `step ${s.step}: [${s.rule}] @${at}  ${canon(s.redex)}  with { ${binds} }`,
  );
  console.log(`   => ${canon(s.result)}`);
}
console.log(`status: ${res.status}, final: ${canon(res.term)}`);
