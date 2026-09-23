import { test } from "node:test";
import assert from "node:assert/strict";
import { booleanLattice, powerSetLattice } from "../src/lattice.ts";
import {
  ComputationRing,
  NonMonotoneError,
  schedulers,
  type Scheduler,
} from "../src/system.ts";
import { Store } from "../src/store.ts";

function reachabilityRing(): ComputationRing {
  // cycle A -> B -> C -> A, plus C -> D; A is the source
  return new ComputationRing()
    .declare("A", booleanLattice, ["C"], (c: boolean) => true || c)
    .declare("B", booleanLattice, ["A"], (a: boolean) => a)
    .declare("C", booleanLattice, ["B"], (b: boolean) => b)
    .declare("D", booleanLattice, ["C"], (c: boolean) => c);
}

test("boolean reachability closure over a cycle converges monotonically", () => {
  const result = reachabilityRing().solve({ trace: true });
  assert.equal(result.status, "converged");
  assert.equal(result.converged, true);
  assert.deepEqual(Object.fromEntries(result.values), {
    A: true,
    B: true,
    C: true,
    D: true,
  });
  for (const entry of result.trace) {
    assert.equal(entry.from, false);
    assert.equal(entry.to, true);
  }
});

test("unreachable nodes stay at bottom", () => {
  const ring = new ComputationRing()
    .declare("A", booleanLattice, [], () => true)
    .declare("B", booleanLattice, ["A"], (a: boolean) => a)
    .declare("X", booleanLattice, ["Y"], (y: boolean) => y)
    .declare("Y", booleanLattice, ["X"], (x: boolean) => x);
  const result = ring.solve();
  assert.equal(result.converged, true);
  assert.deepEqual(Object.fromEntries(result.values), {
    A: true,
    B: true,
    X: false,
    Y: false,
  });
});

test("x = NOT x is rejected as non-monotone instead of spinning forever", () => {
  const ring = new ComputationRing().declare(
    "x",
    booleanLattice,
    ["x"],
    (x: boolean) => !x,
  );
  assert.throws(() => ring.solve(), NonMonotoneError);
});

test("powerset ancestor closure over a cycle reaches the least fixed point", () => {
  const nodes = ["A", "B", "C"] as const;
  const lattice = powerSetLattice<string>(nodes);
  const ring = new ComputationRing()
    .declare("A", lattice, ["C"], (c: ReadonlySet<string>) => new Set(["A", ...c]))
    .declare("B", lattice, ["A"], (a: ReadonlySet<string>) => new Set(["B", ...a]))
    .declare("C", lattice, ["B"], (b: ReadonlySet<string>) => new Set(["C", ...b]));
  const result = ring.solve();
  assert.equal(result.converged, true);
  for (const name of nodes) {
    assert.deepEqual(result.values.get(name), new Set(["A", "B", "C"]));
  }
});

test("different scheduling orders yield the same least fixed point", () => {
  const orders: [string, Scheduler][] = [
    ["fifo", schedulers.fifo],
    ["lifo", schedulers.lifo],
    ["random-1", schedulers.random(1)],
    ["random-42", schedulers.random(42)],
    ["random-2026", schedulers.random(2026)],
  ];
  const baseline = Object.fromEntries(reachabilityRing().solve().values);
  for (const [name, scheduler] of orders) {
    const result = reachabilityRing().solve({ scheduler });
    assert.equal(result.converged, true, name);
    assert.deepEqual(Object.fromEntries(result.values), baseline, name);
  }
});

test("exhausted iteration budget marks the ring as not converged", () => {
  const result = reachabilityRing().solve({ budget: 2 });
  assert.equal(result.status, "exhausted");
  assert.equal(result.converged, false);
  assert.equal(result.evaluations, 2);
});

test("runtime guard catches monotone lies told via assumeMonotone", () => {
  const ring = new ComputationRing()
    .declare("y", booleanLattice, [], () => true)
    .declare("x", booleanLattice, ["y"], (y: boolean) => !y, {
      assumeMonotone: true,
    });
  assert.throws(() => ring.solve({ scheduler: schedulers.lifo }), NonMonotoneError);
});

test("unknown dependencies are rejected", () => {
  const ring = new ComputationRing().declare(
    "a",
    booleanLattice,
    ["ghost"],
    (g: boolean) => g,
  );
  assert.throws(() => ring.solve(), /unknown cell "ghost"/);
});

test("solve commits through the store in one transaction; log replays", () => {
  const store = new Store();
  const before = store.snapshot();
  const result = reachabilityRing().solve({ store });
  assert.equal(result.converged, true);
  assert.equal(store.currentVersion, 1);
  assert.equal(before.get("A"), undefined);
  assert.equal(store.get("A"), true);
  assert.equal(store.get("__status__"), "converged");
  const replayed = Store.replay(store.log);
  for (const name of ["A", "B", "C", "D"]) {
    assert.equal(replayed.get(name), store.get(name));
  }
});
