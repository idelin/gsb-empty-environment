import { test } from "node:test";
import assert from "node:assert/strict";
import { booleanLattice, powerSetLattice } from "../src/lattice.ts";

test("boolean lattice: order, join, bottom", () => {
  assert.equal(booleanLattice.bottom, false);
  assert.equal(booleanLattice.leq(false, true), true);
  assert.equal(booleanLattice.leq(true, false), false);
  assert.equal(booleanLattice.join(false, true), true);
  assert.equal(booleanLattice.join(false, false), false);
  assert.deepEqual(booleanLattice.elements(), [false, true]);
});

test("powerset lattice: subset order, union join, finite carrier", () => {
  const lattice = powerSetLattice(["a", "b", "c"]);
  assert.equal(lattice.bottom.size, 0);
  assert.equal(lattice.elements().length, 8);
  assert.equal(lattice.leq(new Set(["a"]), new Set(["a", "b"])), true);
  assert.equal(lattice.leq(new Set(["a", "b"]), new Set(["a"])), false);
  assert.deepEqual(
    lattice.join(new Set(["a"]), new Set(["b"])),
    new Set(["a", "b"]),
  );
  assert.equal(lattice.equals(new Set(["a", "b"]), new Set(["b", "a"])), true);
});
