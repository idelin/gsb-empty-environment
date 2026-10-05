import assert from "node:assert/strict";
import {
  TermFactory,
  canon,
  v,
  p,
  Engine,
  crossCheck,
  toTerm,
} from "../src/index.ts";
import type { Rule } from "../src/index.ts";
import type { NTree } from "../src/index.ts";

let passed = 0;
function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`ok - ${name}`);
  } catch (err) {
    console.error(`FAIL - ${name}`);
    console.error(err);
    process.exitCode = 1;
  }
}

const fac = new TermFactory();
const c = (op: string) => fac.mk(op);

// ---------------------------------------------------------------------------
// 1. Leftmost-innermost order and rule priority
// ---------------------------------------------------------------------------
test("leftmost-innermost traversal order", () => {
  const rules: Rule[] = [
    { name: "add-zero", lhs: p("add", p("zero"), v("x")), rhs: v("x") },
    { name: "mul-one", lhs: p("mul", p("one"), v("x")), rhs: v("x") },
  ];
  // add(mul(one, a), add(zero, b)): innermost redex is mul(one,a) at path [0]
  const initial = fac.mk("add", [
    fac.mk("mul", [c("one"), c("a")]),
    fac.mk("add", [c("zero"), c("b")]),
  ]);
  const engine = new Engine(rules, initial, fac);
  const res = engine.run();
  assert.equal(res.status, "normal-form");
  assert.deepEqual(
    engine.steps.map((s) => [s.rule, s.path.join(".")]),
    [
      ["mul-one", "0"],
      ["add-zero", "1"],
    ],
  );
  assert.equal(canon(res.term), "add(a,b)");
});

test("higher priority rule wins at the same node", () => {
  const rules: Rule[] = [
    { name: "generic", lhs: p("f", v("x")), rhs: p("c"), priority: 1 },
    { name: "specific", lhs: p("f", p("a")), rhs: p("b"), priority: 10 },
  ];
  const engine = new Engine(rules, fac.mk("f", [c("a")]), fac);
  engine.run();
  assert.equal(engine.steps[0].rule, "specific");
  assert.equal(canon(engine.currentTerm), "b");
});

test("declaration order breaks priority ties", () => {
  const rules: Rule[] = [
    { name: "first", lhs: p("f", v("x")), rhs: p("g", v("x")) },
    { name: "second", lhs: p("f", p("a")), rhs: p("h") },
  ];
  const engine = new Engine(rules, fac.mk("f", [c("a")]), fac);
  engine.run();
  assert.equal(engine.steps[0].rule, "first");
});

// ---------------------------------------------------------------------------
// 2. Variable bindings: consistency and trace output
// ---------------------------------------------------------------------------
test("repeated variable must match equal subterms", () => {
  const rules: Rule[] = [
    { name: "cancel", lhs: p("eq", v("x"), v("x")), rhs: p("yes") },
  ];
  const hit = new Engine(rules, fac.mk("eq", [c("a"), c("a")]), fac);
  hit.run();
  assert.equal(canon(hit.currentTerm), "yes");

  const miss = new Engine(rules, fac.mk("eq", [c("a"), c("b")]), fac);
  const res = miss.run();
  assert.equal(res.status, "normal-form");
  assert.equal(res.steps, 0);
});

test("trace records hit node and bindings", () => {
  const rules: Rule[] = [
    { name: "add-zero", lhs: p("add", p("zero"), v("x")), rhs: v("x") },
  ];
  const initial = fac.mk("wrap", [fac.mk("add", [c("zero"), fac.mk("f", [c("a")])])]);
  const engine = new Engine(rules, initial, fac);
  engine.run();
  const [step] = engine.steps;
  assert.equal(step.rule, "add-zero");
  assert.deepEqual(step.path, [0]);
  assert.equal(canon(step.redex), "add(zero,f(a))");
  assert.deepEqual(
    step.bindings.map(([k, t]) => [k, canon(t)]),
    [["x", "f(a)"]],
  );
});

// ---------------------------------------------------------------------------
// 3. Sharing: logical copies only, no in-place pollution
// ---------------------------------------------------------------------------
test("shared subtrees are logically copied, never polluted", () => {
  const rules: Rule[] = [
    { name: "add-zero", lhs: p("add", p("zero"), v("x")), rhs: v("x") },
  ];
  const shared = fac.mk("add", [c("zero"), c("a")]); // one object, two positions
  const initial = fac.mk("pair", [shared, shared]);
  assert.equal(initial.args[0], initial.args[1]); // physically shared

  const engine = new Engine(rules, initial, fac);
  engine.run(1); // rewrite only the leftmost occurrence
  const after = engine.currentTerm;
  assert.equal(canon(after), "pair(a,add(zero,a))");
  assert.equal(after.args[1], shared); // untouched position keeps the same object
  assert.equal(canon(shared), "add(zero,a)"); // shared node itself unchanged
});

test("template duplication shares by reference (O(1) logical copy)", () => {
  const rules: Rule[] = [
    { name: "dup", lhs: p("dup", v("x")), rhs: p("pair", v("x"), v("x")) },
  ];
  const big = fac.mk("f", [fac.mk("g", [c("a")]), c("b")]);
  const engine = new Engine(rules, fac.mk("dup", [big]), fac);
  engine.run();
  const result = engine.currentTerm;
  assert.equal(canon(result), "pair(f(g(a),b),f(g(a),b))");
  assert.equal(result.args[0], result.args[1]); // shared, not rebuilt
  assert.equal(result.args[0], big);
});

// ---------------------------------------------------------------------------
// 4. Cycle detection
// ---------------------------------------------------------------------------
test("repeated state is reported as a cycle", () => {
  const rules: Rule[] = [
    { name: "f-to-g", lhs: p("f", v("x")), rhs: p("g", v("x")) },
    { name: "g-to-f", lhs: p("g", v("x")), rhs: p("f", v("x")) },
  ];
  const engine = new Engine(rules, fac.mk("f", [c("a")]), fac);
  const res = engine.run();
  assert.equal(res.status, "cycle");
  assert.deepEqual(res.cycle, { firstStep: 0, repeatedAt: 2, state: "f(a)" });
});

test("cycle detection works on non-root states", () => {
  const rules: Rule[] = [
    { name: "swap", lhs: p("p", v("x"), v("y")), rhs: p("p", v("y"), v("x")) },
  ];
  const engine = new Engine(
    rules,
    fac.mk("top", [fac.mk("p", [c("a"), c("b")])]),
    fac,
  );
  const res = engine.run();
  assert.equal(res.status, "cycle");
  assert.equal(res.cycle?.firstStep, 0);
  assert.equal(res.cycle?.repeatedAt, 2);
  assert.deepEqual(engine.steps[0].path, [0]);
});

// ---------------------------------------------------------------------------
// 5. Budget interruption and resumability
// ---------------------------------------------------------------------------
const arith: Rule[] = [
  { name: "add-zero", lhs: p("add", p("zero"), v("x")), rhs: v("x") },
  { name: "mul-one", lhs: p("mul", p("one"), v("x")), rhs: v("x") },
  { name: "neg-neg", lhs: p("neg", p("neg", v("x"))), rhs: v("x") },
];
const arithTerm = () =>
  fac.mk("neg", [
    fac.mk("neg", [
      fac.mk("add", [c("zero"), fac.mk("mul", [c("one"), c("a")])]),
    ]),
  ]);

test("budget interruption is resumable and matches a full run", () => {
  const full = new Engine(arith, arithTerm(), fac);
  const fullRes = full.run();
  assert.equal(fullRes.status, "normal-form");
  assert.equal(fullRes.steps, 3);

  const chunked = new Engine(arith, arithTerm(), fac);
  const r1 = chunked.run(1);
  assert.equal(r1.status, "budget");
  assert.equal(r1.steps, 1);
  const r2 = chunked.run(1);
  assert.equal(r2.status, "budget");
  const r3 = chunked.run(10);
  assert.equal(r3.status, "normal-form");
  assert.equal(r3.steps, 3);
  assert.equal(canon(r3.term), canon(fullRes.term));
  assert.deepEqual(
    chunked.steps.map((s) => [s.rule, s.path.join("."), canon(s.result)]),
    full.steps.map((s) => [s.rule, s.path.join("."), canon(s.result)]),
  );
});

test("snapshot/restore resumes an interrupted run", () => {
  const engine = new Engine(arith, arithTerm(), fac);
  engine.run(1);
  const saved = engine.snapshot();

  const restored = Engine.restore(saved, arith, fac);
  const res = restored.run();
  assert.equal(res.status, "normal-form");
  assert.equal(res.steps, 3);
  assert.equal(canon(res.term), "a");
  const reference = new Engine(arith, arithTerm(), fac);
  reference.run();
  assert.deepEqual(
    restored.steps.map((s) => [s.rule, s.path.join("."), canon(s.result)]),
    reference.steps.map((s) => [s.rule, s.path.join("."), canon(s.result)]),
  );
});

// ---------------------------------------------------------------------------
// 6. Cross-check hash-consed engine vs naive no-sharing interpreter
// ---------------------------------------------------------------------------
test("engine and naive interpreter agree on all scenarios", () => {
  const scenarios: Array<[string, Rule[], NTree]> = [
    [
      "arith",
      arith,
      { op: "neg", args: [{ op: "neg", args: [{ op: "add", args: [{ op: "zero", args: [] }, { op: "mul", args: [{ op: "one", args: [] }, { op: "a", args: [] }] }] }] }] },
    ],
    [
      "priority",
      [
        { name: "generic", lhs: p("f", v("x")), rhs: p("c"), priority: 1 },
        { name: "specific", lhs: p("f", p("a")), rhs: p("b"), priority: 10 },
      ],
      { op: "f", args: [{ op: "a", args: [] }] },
    ],
    [
      "cycle",
      [
        { name: "f-to-g", lhs: p("f", v("x")), rhs: p("g", v("x")) },
        { name: "g-to-f", lhs: p("g", v("x")), rhs: p("f", v("x")) },
      ],
      { op: "f", args: [{ op: "a", args: [] }] },
    ],
    [
      "dup-shared",
      [{ name: "dup", lhs: p("dup", v("x")), rhs: p("pair", v("x"), v("x")) }],
      { op: "dup", args: [{ op: "f", args: [{ op: "g", args: [{ op: "a", args: [] }] }, { op: "b", args: [] }] }] },
    ],
    [
      "consistent-var",
      [{ name: "cancel", lhs: p("eq", v("x"), v("x")), rhs: p("yes") }],
      { op: "eq", args: [{ op: "a", args: [] }, { op: "a", args: [] }] },
    ],
  ];
  for (const [label, rules, initial] of scenarios) {
    const run = crossCheck(label, rules, initial);
    assert.ok(run.steps >= 0);
  }
});

test("cross-check with shared input term equals naive run", () => {
  const rules: Rule[] = [
    { name: "add-zero", lhs: p("add", p("zero"), v("x")), rhs: v("x") },
  ];
  const shared = fac.mk("add", [c("zero"), c("a")]);
  const initial = fac.mk("pair", [shared, shared]);
  const engine = new Engine(rules, initial, fac);
  engine.run();
  const naiveInitial = toTerm(
    JSON.parse(JSON.stringify({ op: "pair", args: [
      { op: "add", args: [{ op: "zero", args: [] }, { op: "a", args: [] }] },
      { op: "add", args: [{ op: "zero", args: [] }, { op: "a", args: [] }] },
    ] })),
    new TermFactory(),
  );
  const naiveEngine = new Engine(rules, naiveInitial, new TermFactory());
  naiveEngine.run();
  assert.equal(canon(engine.currentTerm), canon(naiveEngine.currentTerm));
});

console.log(`\n${passed} tests passed${process.exitCode ? " (with failures)" : ""}`);
