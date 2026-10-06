import assert from "node:assert";
import {
  analyze,
  analyzeMatch,
  ctor,
  enumerateValues,
  Ctx,
  varp,
  _,
} from "../dist/index.js";

const env = {
  Bool: {
    name: "Bool",
    ctors: [
      { name: "True", fields: [] },
      { name: "False", fields: [] },
    ],
  },
  Nat: {
    name: "Nat",
    ctors: [
      { name: "Zero", fields: [] },
      { name: "Succ", fields: ["Nat"] },
    ],
  },
  Option: {
    name: "Option",
    ctors: [
      { name: "None", fields: [] },
      { name: "Some", fields: ["Nat"] },
    ],
  },
  List: {
    name: "List",
    ctors: [
      { name: "Nil", fields: [] },
      { name: "Cons", fields: ["Nat", "List"] },
    ],
  },
  Tree: {
    name: "Tree",
    ctors: [
      { name: "Leaf", fields: [] },
      { name: "Node", fields: ["Tree", "Nat", "Tree"] },
    ],
  },
  Empty: { name: "Empty", ctors: [{ name: "MkEmpty", fields: ["Empty"] }] },
  Even: {
    name: "Even",
    ctors: [
      { name: "EZ", fields: [] },
      { name: "ES", fields: ["Odd"] },
    ],
  },
  Odd: { name: "Odd", ctors: [{ name: "OS", fields: ["Even"] }] },
};

let passed = 0;
function test(name, fn) {
  try {
    fn();
    passed++;
    console.log(`ok - ${name}`);
  } catch (e) {
    console.error(`FAIL - ${name}`);
    console.error(e);
    process.exitCode = 1;
  }
}

const show = (v) => (v.args.length === 0 ? v.ctor : `${v.ctor}(${v.args.map(show).join(",")})`);
const showVec = (vec) => vec.map(show).join(" ");

test("non-exhaustive Option yields minimal witness None (depth 1)", () => {
  const r = analyzeMatch(env, "Option", [ctor("Some", _)]);
  assert.equal(r.exhaustive, false);
  assert.equal(showVec(r.counterexample), "None");
  assert.equal(r.counterexampleDepth, 1);
  assert.equal(r.crossValidation.consistent, true);
});

test("wildcard and variable patterns are equivalent", () => {
  const a = analyzeMatch(env, "Option", [ctor("None"), ctor("Some", _)]);
  const b = analyzeMatch(env, "Option", [ctor("None"), ctor("Some", varp("x"))]);
  assert.equal(a.exhaustive, true);
  assert.equal(b.exhaustive, true);
});

test("branch fully covered by previous branches is unreachable", () => {
  const r = analyzeMatch(env, "Option", [
    ctor("None"),
    ctor("Some", _),
    ctor("Some", ctor("Zero")),
  ]);
  assert.equal(r.exhaustive, true);
  assert.deepEqual(
    r.branches.map((b) => b.status),
    ["reachable", "reachable", "unreachable"]
  );
  assert.deepEqual(r.crossValidation.unreachableUpToDepth, [2]);
  assert.equal(r.crossValidation.consistent, true);
});

test("trailing duplicate wildcard is unreachable", () => {
  const r = analyzeMatch(env, "Bool", [_, _]);
  assert.equal(r.exhaustive, true);
  assert.equal(r.branches[1].status, "unreachable");
});

test("recursive List: minimal counterexample has depth 3", () => {
  const r = analyzeMatch(env, "List", [ctor("Nil"), ctor("Cons", _, ctor("Nil"))]);
  assert.equal(r.exhaustive, false);
  assert.equal(showVec(r.counterexample), "Cons(Zero,Cons(Zero,Nil))");
  assert.equal(r.counterexampleDepth, 3);
  assert.equal(r.crossValidation.consistent, true);
});

test("recursive List: deep nested pattern terminates and is reachable", () => {
  const r = analyzeMatch(env, "List", [
    ctor("Cons", _, ctor("Cons", _, ctor("Cons", ctor("Zero"), _))),
    ctor("Nil"),
    ctor("Cons", _, _),
  ]);
  assert.equal(r.exhaustive, true);
  assert.deepEqual(
    r.branches.map((b) => b.status),
    ["reachable", "reachable", "reachable"]
  );
  assert.equal(r.branches[0].witnessDepth, 4);
  assert.equal(r.crossValidation.consistent, true);
});

test("nested Tree patterns: unreachable nested branch detected", () => {
  const r = analyzeMatch(env, "Tree", [
    ctor("Leaf"),
    ctor("Node", ctor("Leaf"), _, ctor("Leaf")),
    ctor("Node", _, _, _),
    ctor("Node", ctor("Leaf"), ctor("Zero"), _),
  ]);
  assert.equal(r.exhaustive, true);
  assert.equal(r.branches[3].status, "unreachable");
  assert.equal(r.crossValidation.consistent, true);
});

test("Tree non-exhaustive: witness starts with the missing shape", () => {
  const r = analyzeMatch(env, "Tree", [ctor("Leaf"), ctor("Node", ctor("Leaf"), _, _)]);
  assert.equal(r.exhaustive, false);
  assert.equal(showVec(r.counterexample), "Node(Node(Leaf,Zero,Leaf),Zero,Leaf)");
  assert.equal(r.counterexampleDepth, 3);
});

test("multi-scrutinee Bool x Bool", () => {
  const r = analyze(env, ["Bool", "Bool"], [
    [ctor("True"), ctor("True")],
    [ctor("False"), ctor("False")],
  ]);
  assert.equal(r.exhaustive, false);
  const ce = showVec(r.counterexample);
  assert.ok(ce === "True False" || ce === "False True", ce);
  const r2 = analyze(env, ["Bool", "Bool"], [
    [ctor("True"), ctor("True")],
    [ctor("False"), ctor("False")],
    [ctor("True"), _],
    [_, ctor("True")],
  ]);
  assert.equal(r2.exhaustive, true);
  assert.equal(r2.crossValidation.consistent, true);
});

test("uninhabited type: empty match is vacuously exhaustive", () => {
  const r = analyzeMatch(env, "Empty", []);
  assert.equal(r.exhaustive, true);
  const r2 = analyzeMatch(env, "Empty", [ctor("MkEmpty", _)]);
  assert.equal(r2.branches[0].status, "unreachable");
});

test("mutually recursive types: min depths and exhaustiveness", () => {
  const ctx = new Ctx(env);
  assert.equal(ctx.minDepth.get("Even"), 1);
  assert.equal(ctx.minDepth.get("Odd"), 2);
  const r = analyzeMatch(env, "Even", [ctor("EZ"), ctor("ES", ctor("OS", ctor("EZ")))]);
  assert.equal(r.exhaustive, false);
  assert.equal(showVec(r.counterexample), "ES(OS(ES(OS(EZ))))");
  assert.equal(r.counterexampleDepth, 5);
});

test("enumeration: counts and depths for Nat and List", () => {
  const ctx = new Ctx(env);
  const nat = enumerateValues(ctx, ["Nat"], 3);
  assert.equal(nat.byType.get("Nat").length, 3);
  const list = enumerateValues(ctx, ["List"], 2);
  assert.equal(list.byType.get("List").length, 2);
});

test("pattern validation rejects wrong type and arity", () => {
  assert.throws(() => analyzeMatch(env, "Option", [ctor("Nil")]), /belongs to type/);
  assert.throws(() => analyzeMatch(env, "Option", [ctor("Some")]), /expects 1/);
  assert.throws(() => analyzeMatch(env, "Nope", [_]), /unknown scrutinee type/);
});

test("exhaustive check with enum cross-validation disabled", () => {
  const r = analyzeMatch(env, "List", [ctor("Nil"), ctor("Cons", _, _)], {
    enumDepth: 0,
  });
  assert.equal(r.exhaustive, true);
  assert.equal(r.crossValidation, undefined);
});

console.log(`\n${passed} test(s) passed`);
if (process.exitCode) process.exit(process.exitCode);
