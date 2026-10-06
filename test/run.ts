import * as assert from "assert";
import {
  bruteForceOptimum,
  Canvas,
  LabelSpec,
  rectsOverlap,
  solveLayout,
} from "../src";

let passed = 0;
let failed = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`ok   ${name}`);
  } catch (err) {
    failed++;
    console.error(`FAIL ${name}`);
    console.error(err);
  }
}

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const CANVAS: Canvas = { width: 100, height: 100 };

// 1. Touching edges must not count as overlap.
test("edge-touching rects do not overlap", () => {
  assert.strictEqual(
    rectsOverlap(
      { x: 0, y: 0, width: 10, height: 10 },
      { x: 10, y: 0, width: 10, height: 10 },
    ),
    false,
  );
  assert.strictEqual(
    rectsOverlap(
      { x: 0, y: 0, width: 10, height: 10 },
      { x: 10, y: 10, width: 5, height: 5 },
    ),
    false,
  );
  assert.strictEqual(
    rectsOverlap(
      { x: 0, y: 0, width: 10, height: 10 },
      { x: 9.999, y: 0, width: 10, height: 10 },
    ),
    true,
  );
});

// 2. Two labels whose only candidates share an edge are both placed.
test("labels touching at edges are both placed", () => {
  const labels: LabelSpec[] = [
    {
      id: "a",
      anchor: { x: 5, y: 5 },
      weight: 1,
      candidates: [{ rect: { x: 0, y: 0, width: 10, height: 10 } }],
    },
    {
      id: "b",
      anchor: { x: 15, y: 5 },
      weight: 1,
      candidates: [{ rect: { x: 10, y: 0, width: 10, height: 10 } }],
    },
  ];
  const result = solveLayout(labels, CANVAS);
  assert.strictEqual(result.placements.length, 2);
  assert.strictEqual(result.totalWeight, 2);
});

// 3. Strictly overlapping labels: only the heavier one is placed.
test("overlap resolves in favor of higher weight", () => {
  const labels: LabelSpec[] = [
    {
      id: "heavy",
      anchor: { x: 0, y: 0 },
      weight: 5,
      candidates: [{ rect: { x: 0, y: 0, width: 20, height: 10 } }],
    },
    {
      id: "light",
      anchor: { x: 0, y: 0 },
      weight: 3,
      candidates: [{ rect: { x: 5, y: 0, width: 20, height: 10 } }],
    },
  ];
  const result = solveLayout(labels, CANVAS);
  assert.strictEqual(result.placements.length, 1);
  assert.strictEqual(result.placements[0].labelId, "heavy");
  assert.strictEqual(result.totalWeight, 5);
  assert.deepStrictEqual(result.unplaced, ["light"]);
  assert.deepStrictEqual(
    result.conflicts.map((c) => [c.labelA, c.labelB]),
    [["heavy", "light"]],
  );
});

// 4. Candidates outside the canvas are rejected; boundary contact is fine.
test("canvas bounds: outside rejected, boundary accepted", () => {
  const labels: LabelSpec[] = [
    {
      id: "edge",
      anchor: { x: 50, y: 50 },
      weight: 2,
      candidates: [
        { rect: { x: 90, y: 90, width: 10, height: 10 } }, // exactly in corner: ok
        { rect: { x: 91, y: 90, width: 10, height: 10 } }, // sticks out: rejected
      ],
    },
    {
      id: "lost",
      anchor: { x: 1, y: 1 },
      weight: 7,
      candidates: [{ rect: { x: -5, y: 0, width: 10, height: 10 } }],
    },
  ];
  const result = solveLayout(labels, CANVAS);
  assert.strictEqual(result.placements.length, 1);
  assert.strictEqual(result.placements[0].labelId, "edge");
  assert.strictEqual(result.placements[0].candidateIndex, 0);
  assert.deepStrictEqual(result.unplaced, ["lost"]);
});

// 5. Equal weight: the solver minimizes displacement.
test("ties on weight are broken by smaller displacement", () => {
  const labels: LabelSpec[] = [
    {
      id: "a",
      anchor: { x: 10, y: 10 },
      weight: 4,
      candidates: [
        { rect: { x: 60, y: 60, width: 10, height: 10 } }, // far
        { rect: { x: 12, y: 12, width: 10, height: 10 } }, // near
      ],
    },
  ];
  const result = solveLayout(labels, CANVAS);
  assert.strictEqual(result.placements[0].candidateIndex, 1);
  assert.ok(Math.abs(result.totalDisplacement - Math.hypot(2, 2)) < 1e-9);
});

// 6. Hidden labels are never placed, never counted, never blocking.
test("hidden labels cannot fake a successful placement", () => {
  const labels: LabelSpec[] = [
    {
      id: "ghost",
      anchor: { x: 5, y: 5 },
      weight: 1000,
      hidden: true,
      candidates: [{ rect: { x: 0, y: 0, width: 10, height: 10 } }],
    },
    {
      id: "real",
      anchor: { x: 5, y: 5 },
      weight: 1,
      candidates: [{ rect: { x: 0, y: 0, width: 10, height: 10 } }],
    },
  ];
  const result = solveLayout(labels, CANVAS);
  // The hidden label would overlap "real"; if it were (wrongly) treated as
  // placed it would block "real" or inflate the weight by 1000.
  assert.strictEqual(result.placements.length, 1);
  assert.strictEqual(result.placements[0].labelId, "real");
  assert.strictEqual(result.totalWeight, 1);
  assert.deepStrictEqual(result.hidden, ["ghost"]);
  assert.strictEqual(
    result.conflicts.length,
    0,
    "hidden labels must not appear in conflicts",
  );
});

// 7. All labels hidden: empty placement, zero weight.
test("all labels hidden yields an empty, zero-weight result", () => {
  const labels: LabelSpec[] = [
    {
      id: "h1",
      anchor: { x: 0, y: 0 },
      weight: 5,
      hidden: true,
      candidates: [{ rect: { x: 0, y: 0, width: 10, height: 10 } }],
    },
    {
      id: "h2",
      anchor: { x: 0, y: 0 },
      weight: 5,
      hidden: true,
      candidates: [{ rect: { x: 20, y: 20, width: 10, height: 10 } }],
    },
  ];
  const result = solveLayout(labels, CANVAS);
  assert.strictEqual(result.placements.length, 0);
  assert.strictEqual(result.totalWeight, 0);
  assert.strictEqual(result.totalDisplacement, 0);
  assert.deepStrictEqual(result.hidden.sort(), ["h1", "h2"]);
});

// 8. Exhaustive cross-check: solver must match full enumeration.
test("solver matches exhaustive enumeration on random instances", () => {
  const rand = mulberry32(20261007);
  const trials = 300;
  for (let t = 0; t < trials; t++) {
    const labelCount = 1 + Math.floor(rand() * 8); // 1..8 labels
    const labels: LabelSpec[] = [];
    for (let i = 0; i < labelCount; i++) {
      const candidateCount = 1 + Math.floor(rand() * 4); // up to 4 candidates
      const candidates = [];
      for (let c = 0; c < candidateCount; c++) {
        candidates.push({
          rect: {
            x: Math.floor(rand() * 90),
            y: Math.floor(rand() * 90),
            width: 5 + Math.floor(rand() * 20),
            height: 5 + Math.floor(rand() * 20),
          },
        });
      }
      labels.push({
        id: `L${i}`,
        anchor: { x: rand() * 100, y: rand() * 100 },
        weight: 1 + Math.floor(rand() * 10),
        candidates,
        hidden: rand() < 0.15,
      });
    }
    const result = solveLayout(labels, CANVAS);
    const optimum = bruteForceOptimum(labels, CANVAS);
    assert.ok(
      Math.abs(result.totalWeight - optimum.totalWeight) < 1e-9,
      `trial ${t}: weight ${result.totalWeight} != ${optimum.totalWeight}`,
    );
    assert.ok(
      Math.abs(result.totalDisplacement - optimum.totalDisplacement) < 1e-9,
      `trial ${t}: displacement ${result.totalDisplacement} != ${optimum.totalDisplacement}`,
    );
    // Placed rects must be pairwise non-overlapping and inside the canvas.
    for (let i = 0; i < result.placements.length; i++) {
      const ri = result.placements[i].rect;
      assert.ok(
        ri.x >= 0 && ri.y >= 0 &&
          ri.x + ri.width <= CANVAS.width &&
          ri.y + ri.height <= CANVAS.height,
        `trial ${t}: placement out of canvas`,
      );
      for (let j = i + 1; j < result.placements.length; j++) {
        assert.ok(
          !rectsOverlap(ri, result.placements[j].rect),
          `trial ${t}: overlapping placements`,
        );
      }
      // Hidden labels must never appear among placements.
      const spec = labels.find((l) => l.id === result.placements[i].labelId)!;
      assert.ok(!spec.hidden, `trial ${t}: hidden label was placed`);
    }
  }
});

// 9. Full-size smoke test: 12 labels x 4 candidates solves exactly.
test("12 labels x 4 candidates solve quickly and exactly", () => {
  const rand = mulberry32(42);
  const labels: LabelSpec[] = [];
  for (let i = 0; i < 12; i++) {
    const candidates = [];
    for (let c = 0; c < 4; c++) {
      candidates.push({
        rect: {
          x: Math.floor(rand() * 85),
          y: Math.floor(rand() * 85),
          width: 5 + Math.floor(rand() * 15),
          height: 5 + Math.floor(rand() * 15),
        },
      });
    }
    labels.push({
      id: `L${i}`,
      anchor: { x: rand() * 100, y: rand() * 100 },
      weight: 1 + Math.floor(rand() * 10),
      candidates,
    });
  }
  const start = Date.now();
  const result = solveLayout(labels, CANVAS);
  const elapsed = Date.now() - start;
  assert.ok(elapsed < 5000, `solver too slow: ${elapsed}ms`);
  // Spot-check against brute force on the first 8 labels (full 12 would be
  // 5^12 evaluations, too slow for a naive enumerator).
  const optimum = bruteForceOptimum(labels.slice(0, 8), CANVAS);
  const partial = solveLayout(labels.slice(0, 8), CANVAS);
  assert.ok(Math.abs(partial.totalWeight - optimum.totalWeight) < 1e-9);
  assert.ok(result.totalWeight >= partial.totalWeight);
});

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
