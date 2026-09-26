import { test } from "node:test";
import assert from "node:assert/strict";
import { IncrementalSolver } from "../src/incremental.ts";

test("retracting a constraint withdraws its influence", () => {
  const store = new IncrementalSolver([
    { name: "x", lo: 0, hi: 4 },
    { name: "y", lo: 0, hi: 4 },
  ]);
  store.assert({ kind: "diff", id: "gap", a: "x", b: "y", c: -3 }); // y >= x + 3
  const fixId = store.assert({ kind: "eqConst", id: "fix", a: "x", value: 4 }); // then y >= 7

  const before = store.solve();
  assert.equal(before.status, "unsat");

  assert.equal(store.retract(fixId), true);
  assert.equal(store.retract(fixId), false, "second retract is a no-op");

  const after = store.solve();
  assert.equal(after.status, "sat");
  if (after.status === "sat") assert.ok(after.model.y! - after.model.x! >= 3);
});

test("correction: replace a constraint and re-check", () => {
  const store = new IncrementalSolver([{ name: "x", lo: 0, hi: 10 }]);
  const id = store.assert({ kind: "eqConst", a: "x", value: 3 }); // anonymous id assigned
  assert.equal(store.solve().status, "sat");
  store.retract(id);
  store.assert({ kind: "eqConst", a: "x", value: 99 }); // out of domain
  assert.equal(store.solve().status, "unsat");
});

test("independent enumeration cross-checks propagation on sat and unsat", () => {
  const store = new IncrementalSolver([
    { name: "x", lo: 0, hi: 4 },
    { name: "y", lo: 0, hi: 4 },
  ]);
  store.assert({ kind: "diff", id: "gap", a: "x", b: "y", c: -3 });
  const satVerdict = store.verifyByEnumeration();
  assert.deepEqual(satVerdict, { status: "verified", spaceSize: 25, sat: true });

  store.assert({ kind: "eqConst", id: "fix", a: "x", value: 4 });
  const unsatVerdict = store.verifyByEnumeration();
  assert.deepEqual(unsatVerdict, { status: "verified", spaceSize: 25, sat: false });
});

test("enumeration is skipped when the space is too large", () => {
  const store = new IncrementalSolver([{ name: "x", lo: 0, hi: 1_000_000 }]);
  const verdict = store.verifyByEnumeration(100);
  assert.equal(verdict.status, "skipped");
});

test("conditional constraints participate in incremental checks", () => {
  const store = new IncrementalSolver([
    { name: "x", lo: 0, hi: 10 },
    { name: "y", lo: 0, hi: 10 },
  ]);
  store.assert({
    kind: "when",
    id: "w",
    cond: { op: "ge", a: "x", b: 5 },
    then: { kind: "eqConst", id: "w.then", a: "y", value: 0 },
  });
  store.assert({ kind: "eqConst", id: "fx", a: "x", value: 7 });
  store.assert({ kind: "eqConst", id: "fy", a: "y", value: 3 });
  assert.equal(store.solve().status, "unsat", "x >= 5 forces y = 0, contradicting y = 3");
  const verdict = store.verifyByEnumeration();
  assert.deepEqual(verdict, { status: "verified", spaceSize: 121, sat: false });
});
