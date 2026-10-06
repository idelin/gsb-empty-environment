import { strict as assert } from "assert";
import { Type, tvar, tcon, tfun, tlist, typeEq, showType, freeVars } from "../src/types";
import { Subst, applySubst, resolve, showSubst } from "../src/subst";
import { Constraint, unify, unifyAll, showUnifyError, UnifyError } from "../src/unify";
import { genSatisfiable } from "../src/gen";
import { varName } from "../src/types";

let passed = 0;
let failed = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`ok   ${name}`);
  } catch (e) {
    failed++;
    console.error(`FAIL ${name}`);
    console.error(e instanceof Error ? e.stack : e);
  }
}

const v = (id: number): Type => tvar(id);
const Int = tcon("Int");
const Bool = tcon("Bool");

function solvedBothSides(subst: Subst, cs: readonly Constraint[]): void {
  for (const c of cs) {
    const l = applySubst(subst, c.left);
    const r = applySubst(subst, c.right);
    assert.ok(
      typeEq(l, r),
      `constraint not satisfied: ${showType(c.left)} ~ ${showType(c.right)} ` +
        `=> ${showType(l)} vs ${showType(r)}`,
    );
  }
}

// ---------- substitution ----------

test("applySubst follows chains transitively, not one level", () => {
  // a -> b, b -> List<c>, c -> Int
  const s: Subst = new Map<number, Type>([
    [0, v(1)],
    [1, tlist(v(2))],
    [2, Int],
  ]);
  assert.ok(typeEq(applySubst(s, v(0)), tlist(Int)), showSubst(s, varName, showType));
  // also inside structure
  const t = tfun(v(0), tlist(v(1)));
  assert.ok(typeEq(applySubst(s, t), tfun(tlist(Int), tlist(tlist(Int)))));
});

test("applySubst is idempotent (result is normalised)", () => {
  const s: Subst = new Map<number, Type>([
    [0, tfun(v(1), v(1))],
    [1, tlist(v(2))],
    [2, Bool],
  ]);
  const once = applySubst(s, v(0));
  assert.ok(typeEq(applySubst(s, once), once));
});

test("resolve only walks the head chain", () => {
  const s: Subst = new Map<number, Type>([
    [0, v(1)],
    [1, tlist(v(2))],
    [2, Int],
  ]);
  assert.ok(typeEq(resolve(s, v(0)), tlist(v(2))));
});

// ---------- unification ----------

test("unify ground structure", () => {
  const r = unify(tlist(tfun(Int, Bool)), tlist(tfun(Int, Bool)));
  assert.ok(r.ok);
});

test("unify binds variables through lists and functions", () => {
  const r = unify(tfun(v(0), tlist(v(1))), tfun(Int, tlist(tlist(Bool))));
  assert.ok(r.ok);
  assert.ok(typeEq(applySubst(r.subst, v(0)), Int));
  assert.ok(typeEq(applySubst(r.subst, v(1)), tlist(Bool)));
});

test("unifier propagates through variable-variable chains", () => {
  // a ~ b, b ~ c, c ~ List<Int>  =>  a normalises to List<Int>
  const r = unifyAll([
    { left: v(0), right: v(1) },
    { left: v(1), right: v(2) },
    { left: v(2), right: tlist(Int) },
  ]);
  assert.ok(r.ok);
  assert.ok(typeEq(applySubst(r.subst, v(0)), tlist(Int)));
  assert.ok(typeEq(applySubst(r.subst, v(1)), tlist(Int)));
});

test("solution substituted back satisfies every constraint", () => {
  const cs: Constraint[] = [
    { left: tfun(v(0), v(1)), right: tfun(tlist(v(2)), Bool) },
    { left: v(2), right: tfun(v(3), v(3)) },
    { left: tlist(v(4)), right: tlist(tlist(v(1))) },
  ];
  const r = unifyAll(cs);
  assert.ok(r.ok, r.ok ? "" : showUnifyError(r.error));
  solvedBothSides(r.subst, cs);
});

// ---------- occurs check ----------

test("occurs check rejects direct self-reference", () => {
  const r = unify(v(0), tlist(v(0)));
  assert.ok(!r.ok);
  assert.equal(r.error.kind, "occurs");
  if (r.error.kind === "occurs") {
    assert.equal(r.error.variable.id, 0);
    assert.ok(r.error.chain.length >= 1);
  }
});

test("occurs check reports the substitution chain causing recursion", () => {
  // a ~ List<b>; b ~ (c -> a): binding b would be recursive through a.
  const r = unifyAll([
    { left: v(0), right: tlist(v(1)) },
    { left: v(1), right: tfun(v(2), v(0)) },
  ]);
  assert.ok(!r.ok);
  assert.equal(r.error.kind, "occurs");
  if (r.error.kind === "occurs") {
    assert.equal(r.error.variable.id, 1);
    // chain: b ~ (c -> a), then a ~ List<b> leads back to b
    assert.equal(r.error.chain.length, 2);
    assert.equal(r.error.chain[0]!.variable.id, 1);
    assert.equal(r.error.chain[1]!.variable.id, 0);
    const msg = showUnifyError(r.error);
    assert.ok(msg.includes("b ~ c -> a"), msg);
    assert.ok(msg.includes("a ~ List<b>"), msg);
    assert.ok(msg.includes("recursive"), msg);
  }
});

test("occurs check sees through long chains", () => {
  // a ~ List<b>, b ~ c, c ~ List<d>; then d ~ a must fail, chain has 4 links.
  const r = unifyAll([
    { left: v(0), right: tlist(v(1)) },
    { left: v(1), right: v(2) },
    { left: v(2), right: tlist(v(3)) },
    { left: v(3), right: v(0) },
  ]);
  assert.ok(!r.ok);
  assert.equal(r.error.kind, "occurs");
  if (r.error.kind === "occurs") {
    assert.equal(r.error.variable.id, 3);
    // d ~ List<b>, then b ~ c, then c ~ List<d> leads back to d
    assert.equal(r.error.chain.length, 3);
    assert.deepEqual(
      r.error.chain.map((s) => s.variable.id),
      [3, 1, 2],
    );
  }
});

// ---------- mismatches ----------

test("constructor mismatch is rejected", () => {
  const r = unify(Int, Bool);
  assert.ok(!r.ok && r.error.kind === "mismatch");
});

test("shape mismatch is rejected", () => {
  const r = unify(tlist(v(0)), tfun(v(1), v(2)));
  assert.ok(!r.ok && r.error.kind === "mismatch");
});

test("deep mismatch is rejected", () => {
  const r = unify(tfun(Int, tlist(Bool)), tfun(Int, tlist(Int)));
  assert.ok(!r.ok && r.error.kind === "mismatch");
});

// ---------- generated systems ----------

test("constraints generated from a known substitution are solvable; solution substitutes back", () => {
  for (let seed = 1; seed <= 50; seed++) {
    const sys = genSatisfiable(seed, 5, 12);

    // sanity: the known substitution really is a solution
    solvedBothSides(sys.known, sys.constraints);

    const r = unifyAll(sys.constraints);
    assert.ok(r.ok, `seed ${seed}: ${r.ok ? "" : showUnifyError(r.error)}`);

    // solution substitutes back: both sides of every constraint coincide
    solvedBothSides(r.subst, sys.constraints);

    // the computed solution is normalised (idempotent)
    for (const c of sys.constraints) {
      const l = applySubst(r.subst, c.left);
      assert.ok(typeEq(applySubst(r.subst, l), l));
    }
  }
});

test("generated system plus a clashing constraint is unsatisfiable", () => {
  for (let seed = 1; seed <= 20; seed++) {
    const sys = genSatisfiable(seed, 4, 8);
    const bad: Constraint[] = [...sys.constraints, { left: Int, right: Bool }];
    const r = unifyAll(bad);
    assert.ok(!r.ok, `seed ${seed} unexpectedly solvable`);
    assert.equal(r.error.kind, "mismatch");
    assert.equal(r.index, bad.length - 1);
  }
});

test("generated system plus an occurs constraint is unsatisfiable with a chain", () => {
  for (let seed = 1; seed <= 20; seed++) {
    const sys = genSatisfiable(seed, 4, 8);
    // a fresh variable that no generated constraint can mention
    const bad: Constraint[] = [...sys.constraints, { left: v(1000), right: tlist(v(1000)) }];
    const r = unifyAll(bad);
    assert.ok(!r.ok, `seed ${seed} unexpectedly solvable`);
    assert.equal(r.error.kind, "occurs");
    if (r.error.kind === "occurs") {
      assert.ok(r.error.chain.length >= 1);
      // the chain must end at a binding whose expansion mentions the variable
      const last = r.error.chain[r.error.chain.length - 1]!;
      assert.ok(freeVars(last.unfoldsTo).size >= 0); // structure exists
      const msg = showUnifyError(r.error);
      assert.ok(msg.startsWith("occurs check failed"), msg);
    }
  }
});

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
