import { describe, expect, test } from "bun:test";
import { rectsOverlap, rectInsideCanvas } from "../src/geometry";
import {
  Label,
  computeConflictPairs,
  fourCandidates,
  solveLabelLayout,
} from "../src/solver";
import { verifyLayout } from "../src/verify";

const canvas = { x: 0, y: 0, w: 100, h: 100 };

describe("geometry", () => {
  test("touching edges do not count as overlap", () => {
    const a = { x: 0, y: 0, w: 10, h: 10 };
    expect(rectsOverlap(a, { x: 10, y: 0, w: 10, h: 10 })).toBe(false); // edge
    expect(rectsOverlap(a, { x: 10, y: 10, w: 5, h: 5 })).toBe(false); // corner
    expect(rectsOverlap(a, { x: 9.999, y: 0, w: 10, h: 10 })).toBe(true);
  });

  test("canvas boundary is inclusive", () => {
    expect(rectInsideCanvas({ x: 0, y: 0, w: 100, h: 100 }, canvas)).toBe(true);
    expect(rectInsideCanvas({ x: -0.5, y: 0, w: 100, h: 100 }, canvas)).toBe(false);
    expect(rectInsideCanvas({ x: 0, y: 0, w: 100.5, h: 100 }, canvas)).toBe(false);
  });
});

describe("solver", () => {
  test("higher weight wins a direct conflict", () => {
    // Every candidate of both labels is the same rect, so at most one fits.
    const anchor = { x: 50, y: 50 };
    const spot = { x: 40, y: 45, w: 20, h: 10 };
    const labels: Label[] = [
      { id: "heavy", anchor, weight: 10, candidates: [spot, spot, spot, spot] },
      { id: "light", anchor, weight: 1, candidates: [spot, spot, spot, spot] },
    ];
    const r = solveLabelLayout({ canvas, labels });
    expect(r.placed.map((p) => p.id)).toEqual(["heavy"]);
    expect(r.unplaced).toEqual(["light"]);
    expect(r.totalWeight).toBe(10);
  });

  test("two light labels that out-weight one heavy label both get placed", () => {
    // Every heavy candidate is a wide band covering both lights' only
    // feasible candidates, so placing heavy hides both lights (and the
    // reverse does not hold: the two lights never touch each other).
    const band = (dy: number) => ({ x: 5, y: 35 + dy, w: 90, h: 30 });
    const labels: Label[] = [
      {
        id: "heavy",
        anchor: { x: 50, y: 50 },
        weight: 9,
        candidates: [band(0), band(-5), band(5), band(0)],
      },
      { id: "lightA", anchor: { x: 15, y: 50 }, weight: 5, candidates: fourCandidates({ x: 15, y: 50 }, 10, 10, 1) },
      { id: "lightB", anchor: { x: 85, y: 50 }, weight: 5, candidates: fourCandidates({ x: 85, y: 50 }, 10, 10, 1) },
    ];
    const r = solveLabelLayout({ canvas, labels });
    expect(r.totalWeight).toBe(10);
    expect(r.unplaced).toEqual(["heavy"]);

    // Flip the priority: heavy outweighs both lights combined.
    labels[0].weight = 11;
    const r2 = solveLabelLayout({ canvas, labels });
    expect(r2.totalWeight).toBe(11);
    expect(r2.unplaced).toEqual(["lightA", "lightB"]);
  });

  test("displacement breaks ties at equal weight", () => {
    // Single label, four candidates at different distances from the anchor.
    const anchor = { x: 50, y: 50 };
    const labels: Label[] = [
      {
        id: "solo",
        anchor,
        weight: 3,
        candidates: [
          { x: 60, y: 50, w: 10, h: 10 }, // distance 10
          { x: 52, y: 50, w: 10, h: 10 }, // distance 2  <- cheapest
          { x: 55, y: 50, w: 10, h: 10 }, // distance 5
          { x: 70, y: 50, w: 10, h: 10 }, // distance 20
        ],
      },
    ];
    const r = solveLabelLayout({ canvas, labels });
    expect(r.placed).toHaveLength(1);
    expect(r.placed[0].candidateIndex).toBe(1);
    expect(r.totalDisplacement).toBeCloseTo(2, 9);
  });

  test("candidates outside the canvas are never used", () => {
    const anchor = { x: 2, y: 2 };
    const labels: Label[] = [
      { id: "corner", anchor, weight: 5, candidates: fourCandidates(anchor, 10, 10, 1) },
    ];
    const r = solveLabelLayout({ canvas, labels });
    expect(r.placed).toHaveLength(1);
    expect(r.placed[0].candidateIndex).toBe(2); // bottom-right is the only fit
  });

  test("hidden labels are reported, never counted as placed", () => {
    const anchor = { x: 50, y: 50 };
    // All candidates of both labels are the same rect: only one can fit.
    const spot = { x: 40, y: 45, w: 20, h: 10 };
    const labels: Label[] = [
      { id: "a", anchor, weight: 5, candidates: [spot, spot, spot, spot] },
      { id: "b", anchor, weight: 5, candidates: [spot, spot, spot, spot] },
    ];
    const r = solveLabelLayout({ canvas, labels });
    expect(r.placed).toHaveLength(1);
    expect(r.unplaced).toHaveLength(1);
    expect(r.totalWeight).toBe(5);
    const report = verifyLayout({ canvas, labels }, r);
    expect(report.errors).toEqual([]);
    expect(report.ok).toBe(true);
  });

  test("conflict pair detection respects touching edges", () => {
    const labels: Label[] = [
      { id: "a", anchor: { x: 0, y: 0 }, weight: 1, candidates: [{ x: 0, y: 0, w: 10, h: 10 }] },
      { id: "b", anchor: { x: 0, y: 0 }, weight: 1, candidates: [{ x: 10, y: 0, w: 10, h: 10 }] },
      { id: "c", anchor: { x: 0, y: 0 }, weight: 1, candidates: [{ x: 5, y: 0, w: 10, h: 10 }] },
    ];
    const conflicts = computeConflictPairs(labels);
    expect(conflicts.find((c) => c.a === "a" && c.b === "b")).toBeUndefined();
    expect(conflicts.find((c) => c.a === "a" && c.b === "c")).toBeDefined();
  });
});

describe("exhaustive verification", () => {
  // Deterministic PRNG (mulberry32) for reproducible random instances.
  function rng(seed: number): () => number {
    let s = seed >>> 0;
    return () => {
      s = (s + 0x6d2b79f5) >>> 0;
      let t = s;
      t = Math.imul(t ^ (t >>> 15), t | 1);
      t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }

  function randomInstance(seed: number, count: number) {
    const rand = rng(seed);
    const labels: Label[] = [];
    for (let i = 0; i < count; i++) {
      const anchor = { x: rand() * 90 + 5, y: rand() * 90 + 5 };
      labels.push({
        id: `r${i}`,
        anchor,
        weight: Math.round(rand() * 9 + 1),
        candidates: fourCandidates(anchor, 8 + rand() * 14, 6 + rand() * 8, rand() * 3),
      });
    }
    return { canvas, labels };
  }

  test("solver matches exhaustive optimum on random instances", () => {
    for (let seed = 1; seed <= 30; seed++) {
      const input = randomInstance(seed, 8);
      const result = solveLabelLayout(input);
      const report = verifyLayout(input, result);
      expect(report.errors).toEqual([]);
      expect(report.ok).toBe(true);
    }
  });

  test("solver matches exhaustive optimum with twelve labels", () => {
    const input = randomInstance(1234, 12);
    const result = solveLabelLayout(input);
    const report = verifyLayout(input, result);
    expect(report.errors).toEqual([]);
    expect(report.ok).toBe(true);
  });

  test("verifier rejects a forged result where a hidden label claims placement", () => {
    const anchor = { x: 50, y: 50 };
    const spot = { x: 40, y: 45, w: 20, h: 10 };
    const labels: Label[] = [
      { id: "a", anchor, weight: 5, candidates: [spot, spot, spot, spot] },
      { id: "b", anchor, weight: 5, candidates: [spot, spot, spot, spot] },
    ];
    const real = solveLabelLayout({ canvas, labels });
    // Forge: pretend the hidden label was also placed (overlapping "a").
    const forged = {
      ...real,
      placed: [
        ...real.placed,
        {
          id: real.unplaced[0],
          candidateIndex: 0,
          rect: labels[1].candidates[0],
          weight: 5,
          displacement: 0,
        },
      ],
      unplaced: [] as string[],
      totalWeight: 10,
    };
    const report = verifyLayout({ canvas, labels }, forged);
    expect(report.ok).toBe(false);
    expect(report.errors.some((e) => e.includes("overlap"))).toBe(true);
  });
});
