import type { Pipeline } from "../src/dag.ts";

/** A: round half-up; B: round half-even. */
export const roundA: Pipeline = {
  name: "A(half-up)",
  nodes: [
    { id: "x", op: "input", slot: "x" },
    { id: "r", op: "round", a: "x", places: 2, mode: "half-up" },
    { id: "out", op: "output", a: "r", name: "total" },
  ],
};
export const roundB: Pipeline = {
  name: "B(half-even)",
  nodes: [
    { id: "x", op: "input", slot: "x" },
    { id: "r", op: "round", a: "x", places: 2, mode: "half-even" },
    { id: "out", op: "output", a: "r", name: "total" },
  ],
};

/** Mean with /N vs /(N-1), deliberately re-associated sums. */
export const meanA: Pipeline = {
  name: "A(/N)",
  nodes: [
    { id: "a", op: "input", slot: "a" },
    { id: "b", op: "input", slot: "b" },
    { id: "c", op: "input", slot: "c" },
    { id: "s1", op: "add", a: "a", b: "b" },
    { id: "s2", op: "add", a: "s1", b: "c" },
    { id: "n", op: "const", value: "3", label: "count" },
    { id: "m", op: "div", a: "s2", b: "n" },
    { id: "out", op: "output", a: "m", name: "mean" },
  ],
};
export const meanB: Pipeline = {
  name: "B(/(N-1))",
  nodes: [
    { id: "a", op: "input", slot: "a" },
    { id: "b", op: "input", slot: "b" },
    { id: "c", op: "input", slot: "c" },
    { id: "s", op: "add", a: "c", b: "b" },
    { id: "s2", op: "add", a: "a", b: "s" },
    { id: "n", op: "const", value: "2", label: "count" },
    { id: "m", op: "div", a: "s2", b: "n" },
    { id: "out", op: "output", a: "m", name: "mean" },
  ],
};
