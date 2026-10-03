/**
 * Runnable demos: each scenario prints an exact divergence report.
 * Run: node src/cli.ts   (Node >= 23.6, type stripping)
 */
import type { Pipeline } from "./dag.ts";
import { comparePipelines } from "./compare.ts";
import { compareAndConfirm } from "./counterfactual.ts";
import { searchMinimalCounterexample } from "./search.ts";
import { SI_TABLE } from "./units.ts";
import { Rational } from "./rational.ts";

function show(title: string, data: unknown): void {
  console.log(`\n=== ${title} ${"=".repeat(Math.max(4, 60 - title.length))}`);
  console.log(JSON.stringify(data, null, 2));
}

// ---------------------------------------------------------------- 1. rounding
const roundA: Pipeline = {
  name: "A(half-up)",
  nodes: [
    { id: "x", op: "input", slot: "x" },
    { id: "r", op: "round", a: "x", places: 2, mode: "half-up" },
    { id: "out", op: "output", a: "r", name: "total" },
  ],
};
const roundB: Pipeline = {
  name: "B(half-even)",
  nodes: [
    { id: "x", op: "input", slot: "x" },
    { id: "r", op: "round", a: "x", places: 2, mode: "half-even" },
    { id: "out", op: "output", a: "r", name: "total" },
  ],
};
{
  const { report, counterfactuals } = compareAndConfirm(roundA, roundB, { x: "0.125" });
  show("1. rounding-mode divergence @ x=0.125", report);
  show("1b. counterfactual: give A B's rounding rule, recompute", counterfactuals);
}

// ----------------------------------------------------------- 2. denominator
const meanA: Pipeline = {
  name: "A(population mean, /N)",
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
const meanB: Pipeline = {
  name: "B(sample convention, /(N-1))",
  nodes: [
    { id: "a", op: "input", slot: "a" },
    { id: "b", op: "input", slot: "b" },
    { id: "c", op: "input", slot: "c" },
    { id: "s", op: "add", a: "c", b: "b" }, // different but equivalent grouping
    { id: "s2", op: "add", a: "a", b: "s" },
    { id: "n", op: "const", value: "2", label: "count" },
    { id: "m", op: "div", a: "s2", b: "n" },
    { id: "out", op: "output", a: "m", name: "mean" },
  ],
};
{
  const { report, counterfactuals } = compareAndConfirm(meanA, meanB, {
    a: "1",
    b: "2",
    c: "4",
  });
  show("2. denominator convention /N vs /(N-1) (DAGs re-associated)", report);
  show("2b. counterfactual: give A B's denominator", counterfactuals);
}

// ------------------------------------------------------- 3. unit conversion
const inchA: Pipeline = {
  name: "A(exact 1in=0.0254m)",
  nodes: [
    { id: "x", op: "input", slot: "x", unit: "in" },
    { id: "cm", op: "convert", a: "x", unit: "cm" },
    { id: "out", op: "output", a: "cm", name: "length" },
  ],
};
const approxTable = {
  ...SI_TABLE,
  in: { name: "in", dims: { m: 1 }, scale: Rational.parse("1/40") }, // 0.025 approx
};
const inchB: Pipeline = {
  name: "B(approx 1in=0.025m)",
  unitTable: approxTable,
  nodes: [
    { id: "x", op: "input", slot: "x", unit: "in" },
    { id: "cm", op: "convert", a: "x", unit: "cm" },
    { id: "out", op: "output", a: "cm", name: "length" },
  ],
};
{
  const { report, counterfactuals } = compareAndConfirm(inchA, inchB, { x: "12" });
  show("3. unit-conversion-factor divergence (12 in)", report);
  show("3b. counterfactual: give A B's inch factor", counterfactuals);
}

// ------------------------------- 4. finals equal, intermediates differ
const twoStageA: Pipeline = {
  name: "A(round 2dp then 2dp)",
  nodes: [
    { id: "x", op: "input", slot: "x" },
    { id: "r1", op: "round", a: "x", places: 2, mode: "half-up" },
    { id: "r2", op: "round", a: "r1", places: 2, mode: "half-up" },
    { id: "out", op: "output", a: "r2", name: "total" },
  ],
};
const twoStageB: Pipeline = {
  name: "B(round 3dp then 2dp)",
  nodes: [
    { id: "x", op: "input", slot: "x" },
    { id: "r1", op: "round", a: "x", places: 3, mode: "half-up" },
    { id: "r2", op: "round", a: "r1", places: 2, mode: "half-up" },
    { id: "out", op: "output", a: "r2", name: "total" },
  ],
};
{
  const report = comparePipelines(twoStageA, twoStageB, { x: "0.1244" });
  show("4. final equal but intermediate differs @ x=0.1244", report);
}

// ------------------------------------ 5. missing intermediate evidence
{
  const report = comparePipelines(
    roundA,
    roundB,
    { x: "0.125" },
    { keepTraceB: (id) => id === "x" || id === "out" } // B hides its rounding stage
  );
  show("5. outputs differ, B's rounding stage hidden -> cannot-locate", report);
}

// ---------------------------------------------------- 6. counterexample search
{
  const found = searchMinimalCounterexample(
    roundA,
    roundB,
    { slots: ["x"], maxNum: 1, maxDen: 200, includeNegative: false, includeZero: false },
    50000
  );
  show("6. minimal counterexample search: half-up vs half-even", {
    status: found.status,
    tested: found.tested,
    totalCandidates: found.totalCandidates,
    counterexample: found.counterexample,
    firstDivergence: found.compareReport?.firstDivergence,
    rootCauses: found.compareReport?.rootCauses,
  });

  const undecided = searchMinimalCounterexample(
    roundA,
    roundB,
    { slots: ["x"], maxNum: 1, maxDen: 200, includeNegative: false, includeZero: false },
    5 // deliberately too small: the minimal witness 1/8 is candidate #8
  );
  show("6b. same search with budget 5 -> undecided", {
    status: undecided.status,
    tested: undecided.tested,
    totalCandidates: undecided.totalCandidates,
  });
}
