import assert from "node:assert/strict";
import {
  tvar, tfun, tlist, INT, BOOL, STR,
  showType, typeEquals, occursVar, varName,
  applySubst, composeSubst, unify,
} from "../dist/index.js";

function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randInt(rand, lo, hi) {
  return lo + Math.floor(rand() * (hi - lo + 1));
}

const CONS = [INT, BOOL, STR];

function genType(rand, varLo, varHi, depth) {
  const canVar = varHi >= varLo;
  if (depth === 0 || rand() < 0.45) {
    if (canVar && rand() < 0.6) return tvar(randInt(rand, varLo, varHi));
    return CONS[randInt(rand, 0, CONS.length - 1)];
  }
  if (rand() < 0.5) {
    return tfun(genType(rand, varLo, varHi, depth - 1), genType(rand, varLo, varHi, depth - 1));
  }
  return tlist(genType(rand, varLo, varHi, depth - 1));
}

function genSubst(rand, numVars) {
  const subst = new Map();
  for (let i = 0; i < numVars; i++) {
    if (rand() < 0.6) {
      subst.set(i, genType(rand, i + 1, numVars - 1, randInt(rand, 0, 2)));
    }
  }
  return subst;
}

function genConstraints(rand, subst, numVars) {
  const constraints = [];
  for (const [id, t] of subst) {
    constraints.push({ left: tvar(id), right: t });
  }
  const extra = randInt(rand, 2, 6);
  for (let k = 0; k < extra; k++) {
    const t = genType(rand, 0, numVars - 1, randInt(rand, 1, 3));
    const img = applySubst(subst, t);
    if (rand() < 0.5) constraints.push({ left: t, right: img });
    else constraints.push({ left: img, right: t });
  }
  for (let i = constraints.length - 1; i > 0; i--) {
    const j = randInt(rand, 0, i);
    const tmp = constraints[i];
    constraints[i] = constraints[j];
    constraints[j] = tmp;
  }
  return constraints;
}

function testBasics() {
  assert.equal(showType(tfun(tvar(0), tlist(INT))), "(a -> List<Int>)");
  assert.equal(varName(25), "z");
  assert.equal(varName(26), "aa");

  const empty = unify([]);
  assert.ok(empty.ok);
  assert.equal(empty.subst.size, 0);

  const simple = unify([{ left: tvar(0), right: tfun(INT, tlist(BOOL)) }]);
  assert.ok(simple.ok);
  assert.ok(typeEquals(applySubst(simple.subst, tvar(0)), tfun(INT, tlist(BOOL))));

  const decompose = unify([
    { left: tfun(tvar(0), tlist(tvar(1))), right: tfun(tlist(INT), tlist(tvar(2))) },
  ]);
  assert.ok(decompose.ok);
  assert.ok(typeEquals(applySubst(decompose.subst, tvar(0)), tlist(INT)));
  assert.ok(typeEquals(applySubst(decompose.subst, tvar(2)), tvar(1)));
  console.log("ok - basics");
}

function testTransitiveSubstitution() {
  const result = unify([
    { left: tvar(0), right: tvar(1) },
    { left: tvar(1), right: tvar(2) },
    { left: tvar(2), right: tlist(tfun(INT, BOOL)) },
  ]);
  assert.ok(result.ok);
  const expected = tlist(tfun(INT, BOOL));
  assert.ok(typeEquals(applySubst(result.subst, tvar(0)), expected));
  assert.ok(typeEquals(applySubst(result.subst, tvar(1)), expected));
  assert.ok(typeEquals(result.subst.get(0), expected), "normalized range must be fully resolved");
  assert.ok(typeEquals(result.subst.get(1), expected), "normalized range must be fully resolved");

  const inner = new Map([[0, tvar(1)]]);
  const outer = new Map([[1, tlist(INT)]]);
  const composed = composeSubst(outer, inner);
  assert.ok(typeEquals(applySubst(composed, tvar(0)), tlist(INT)));
  console.log("ok - transitive substitution");
}

function expectOccurs(constraints, minChainLength) {
  const result = unify(constraints);
  assert.ok(!result.ok, "expected unification to fail");
  assert.equal(result.error.kind, "occurs");
  const chain = result.error.chain;
  assert.ok(chain.length >= minChainLength, "chain too short:\n" + result.error.message);
  const attempted = chain[0];
  const pathSubst = new Map();
  for (let k = 1; k < chain.length; k++) {
    pathSubst.set(chain[k].variable.id, chain[k].type);
  }
  const expanded = applySubst(pathSubst, attempted.type);
  assert.ok(
    occursVar(attempted.variable.id, expanded),
    "chain does not close a loop back to " + varName(attempted.variable.id),
  );
  assert.ok(result.error.message.includes("occurs check failed"));
  return result.error;
}

function testUnsatisfiable() {
  expectOccurs([{ left: tvar(0), right: tlist(tvar(0)) }], 1);
  expectOccurs([{ left: tvar(0), right: tfun(tvar(0), INT) }], 1);

  const two = expectOccurs([
    { left: tvar(0), right: tlist(tvar(1)) },
    { left: tvar(1), right: tfun(tvar(0), INT) },
  ], 2);
  assert.ok(two.message.includes(":="));

  expectOccurs([
    { left: tvar(0), right: tlist(tvar(1)) },
    { left: tvar(1), right: tfun(tvar(2), INT) },
    { left: tvar(2), right: tlist(tvar(0)) },
  ], 3);

  const rigid = unify([{ left: INT, right: BOOL }]);
  assert.ok(!rigid.ok && rigid.error.kind === "mismatch");
  assert.ok(rigid.error.message.includes("Int") && rigid.error.message.includes("Bool"));

  const shape = unify([{ left: tlist(INT), right: tfun(tvar(0), tvar(1)) }]);
  assert.ok(!shape.ok && shape.error.kind === "mismatch");

  const deep = unify([
    { left: tvar(0), right: tlist(tvar(1)) },
    { left: tvar(1), right: INT },
    { left: tvar(0), right: tlist(BOOL) },
  ]);
  assert.ok(!deep.ok && deep.error.kind === "mismatch");

  console.log("ok - unsatisfiable samples");
  console.log("sample occurs error message:\n" + two.message);
}

function testSatisfiableProperty() {
  const rounds = 300;
  for (let seed = 1; seed <= rounds; seed++) {
    const rand = mulberry32(seed);
    const numVars = randInt(rand, 1, 6);
    const known = genSubst(rand, numVars);
    const constraints = genConstraints(rand, known, numVars);
    const result = unify(constraints);
    assert.ok(
      result.ok,
      "seed " + seed + ": expected unifiable, got:\n" + (result.ok ? "" : result.error.message),
    );
    const solution = result.subst;
    for (const c of constraints) {
      const l = applySubst(solution, c.left);
      const r = applySubst(solution, c.right);
      assert.ok(
        typeEquals(l, r),
        "seed " + seed + ": solution does not satisfy " +
          showType(c.left) + " = " + showType(c.right) +
          " (got " + showType(l) + " vs " + showType(r) + ")",
      );
    }
    for (const [id, t] of solution) {
      assert.ok(
        typeEquals(applySubst(solution, t), t),
        "seed " + seed + ": range of " + varName(id) + " is not fully resolved",
      );
    }
  }
  console.log("ok - satisfiable property (" + rounds + " seeded rounds)");
}

testBasics();
testTransitiveSubstitution();
testUnsatisfiable();
testSatisfiableProperty();
console.log("all tests passed");
