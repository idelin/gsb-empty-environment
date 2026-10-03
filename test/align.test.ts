import { test } from "node:test";
import assert from "node:assert/strict";
import { alignPipelines } from "../src/align.ts";
import type { Pipeline } from "../src/dag.ts";

test("reassociated and commuted sums align", () => {
  const a: Pipeline = {
    name: "A",
    nodes: [
      { id: "x", op: "input", slot: "x" },
      { id: "y", op: "input", slot: "y" },
      { id: "z", op: "input", slot: "z" },
      { id: "s1", op: "add", a: "x", b: "y" },
      { id: "s2", op: "add", a: "s1", b: "z" },
      { id: "out", op: "output", a: "s2", name: "o" },
    ],
  };
  const b: Pipeline = {
    name: "B",
    nodes: [
      { id: "x", op: "input", slot: "x" },
      { id: "y", op: "input", slot: "y" },
      { id: "z", op: "input", slot: "z" },
      { id: "t1", op: "add", a: "z", b: "y" }, // commuted
      { id: "t2", op: "add", a: "x", b: "t1" }, // re-associated
      { id: "out", op: "output", a: "t2", name: "o" },
    ],
  };
  const al = alignPipelines(a, b);
  const pair = (id: string): string | undefined =>
    al.pairs.find((p) => p.aId === id)?.bId;
  assert.equal(pair("s2"), "t2"); // (x+y)+z aligns with x+(z+y)
  assert.equal(pair("out"), "out");
  // s1/t1 are different subterms: they stay unmatched, and that is fine
  assert.deepEqual(al.unmatchedA, ["s1"]);
  assert.deepEqual(al.unmatchedB, ["t1"]);
});

test("sub(a,b) aligns with add(a, neg(b)); double negation cancels", () => {
  const a: Pipeline = {
    name: "A",
    nodes: [
      { id: "x", op: "input", slot: "x" },
      { id: "y", op: "input", slot: "y" },
      { id: "d", op: "sub", a: "x", b: "y" },
      { id: "out", op: "output", a: "d", name: "o" },
    ],
  };
  const b: Pipeline = {
    name: "B",
    nodes: [
      { id: "x", op: "input", slot: "x" },
      { id: "y", op: "input", slot: "y" },
      { id: "ny", op: "neg", a: "y" },
      { id: "nny", op: "neg", a: "ny" },
      { id: "nnny", op: "neg", a: "nny" }, // = neg(y)
      { id: "d", op: "add", a: "x", b: "nnny" },
      { id: "out", op: "output", a: "d", name: "o" },
    ],
  };
  const al = alignPipelines(a, b);
  assert.equal(al.pairs.find((p) => p.aId === "d")?.bId, "d");
});

test("alignment is not positional: extra stage in the middle does not shift pairs", () => {
  const a: Pipeline = {
    name: "A",
    nodes: [
      { id: "x", op: "input", slot: "x" },
      { id: "extra", op: "round", a: "x", places: 5, mode: "half-up" },
      { id: "d", op: "mul", a: "extra", b: "extra" },
      { id: "out", op: "output", a: "d", name: "o" },
    ],
  };
  const b: Pipeline = {
    name: "B",
    nodes: [
      { id: "x", op: "input", slot: "x" },
      { id: "d", op: "mul", a: "x", b: "x" },
      { id: "out", op: "output", a: "d", name: "o" },
    ],
  };
  const al = alignPipelines(a, b);
  // x aligns with x even though B's node #2 is A's node #3
  assert.equal(al.pairs.find((p) => p.aId === "x")?.bId, "x");
  assert.deepEqual(al.unmatchedA, ["extra", "d"]);
});
