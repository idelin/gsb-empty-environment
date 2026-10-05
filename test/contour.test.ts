import { describe, expect, test } from "bun:test";
import {
  extractContours,
  Rational,
  type ContourResult,
  type Grid,
} from "../src/index";

const grid = (width: number, height: number, rows: number[][]): Grid => ({
  width,
  height,
  data: rows.flat(),
});

const key = (p: { x: Rational; y: Rational }): string =>
  `${p.x.toString()},${p.y.toString()}`;

const segmentKeys = (result: ContourResult): string[] =>
  result.segments.map((s) => {
    const ka = key(result.points[s.a]);
    const kb = key(result.points[s.b]);
    return ka < kb ? `${ka}|${kb}` : `${kb}|${ka}`;
  });

/** Structural invariants that must hold for every extraction. */
function assertWellFormed(
  result: ContourResult,
  width: number,
  height: number,
  options: { expectBoundaryChainEnds?: boolean } = {},
): void {
  // Points are unique by exact rational coordinate.
  const pointKeys = new Set(result.points.map(key));
  expect(pointKeys.size).toBe(result.points.length);

  // No zero-length and no duplicated (undirected) segments.
  const segKeys = new Set(segmentKeys(result));
  expect(segKeys.size).toBe(result.segments.length);
  for (const s of result.segments) {
    expect(s.a).not.toBe(s.b);
  }

  // Assembly consumes every segment exactly once.
  let consumed = 0;
  for (const c of result.contours) {
    consumed += c.closed ? c.points.length : c.points.length - 1;
  }
  expect(consumed).toBe(result.segments.length);

  // Degree of every point; odd-degree points (chain ends) only on the boundary.
  const idByKey = new Map(result.points.map((p, i) => [key(p), i]));
  const degree = new Array<number>(result.points.length).fill(0);
  for (const s of result.segments) {
    degree[s.a]++;
    degree[s.b]++;
  }
  for (let i = 0; i < result.points.length; i++) {
    const p = result.points[i];
    const x = p.x.toNumber();
    const y = p.y.toNumber();
    expect(x).toBeGreaterThanOrEqual(0);
    expect(x).toBeLessThanOrEqual(width - 1);
    expect(y).toBeGreaterThanOrEqual(0);
    expect(y).toBeLessThanOrEqual(height - 1);
    // With exact-threshold hits, merged flat-edge contours may legitimately
    // leave odd-degree junction points in the interior.
    if (options.expectBoundaryChainEnds && degree[i] % 2 === 1) {
      const onBoundary = x === 0 || y === 0 || x === width - 1 || y === height - 1;
      expect(onBoundary).toBe(true);
    }
  }

  // Every contour vertex is a known point; loops have no repeated start/end.
  for (const c of result.contours) {
    expect(c.points.length).toBeGreaterThanOrEqual(c.closed ? 3 : 2);
    for (const p of c.points) {
      expect(idByKey.has(key(p))).toBe(true);
    }
    if (c.closed) {
      expect(key(c.points[0])).not.toBe(key(c.points[c.points.length - 1]));
    }
  }
}

describe("Rational", () => {
  test("normalizes sign and reduces", () => {
    expect(Rational.ratio(2n, 4n).toString()).toBe("1/2");
    expect(Rational.ratio(1n, -2n).toString()).toBe("-1/2");
    expect(Rational.ratio(-3n, -9n).toString()).toBe("1/3");
    expect(Rational.ratio(0n, 5n).toString()).toBe("0");
  });

  test("parses numbers exactly", () => {
    expect(Rational.of(3).toString()).toBe("3");
    expect(Rational.of(2.5).toString()).toBe("5/2");
    expect(Rational.of(0.1).toString()).toBe("1/10");
    expect(Rational.of(-1.25).toString()).toBe("-5/4");
  });

  test("arithmetic and comparison are exact", () => {
    const third = Rational.ratio(1n, 3n);
    const sixth = Rational.ratio(1n, 6n);
    expect(third.add(sixth).toString()).toBe("1/2");
    expect(third.mul(sixth).toString()).toBe("1/18");
    expect(third.compare(sixth)).toBe(1);
    expect(Rational.of(2).div(Rational.of(4)).toString()).toBe("1/2");
  });
});

describe("extractContours", () => {
  test("single peak: shared-edge crossings are generated exactly once", () => {
    const g = grid(3, 3, [
      [0, 0, 0],
      [0, 4, 0],
      [0, 0, 0],
    ]);
    const r = extractContours(g, 2);
    // Four edges cross the threshold; each is shared by two cells.
    expect(r.stats.edgeInterpolations).toBe(4);
    expect(r.points.length).toBe(4);
    expect(r.segments.length).toBe(4);
    expect(r.contours.length).toBe(1);
    const loop = r.contours[0];
    expect(loop.closed).toBe(true);
    expect(loop.points.length).toBe(4);
    expect(new Set(loop.points.map(key))).toEqual(
      new Set(["1,1/2", "3/2,1", "1,3/2", "1/2,1"]),
    );
    assertWellFormed(r, 3, 3);
  });

  test("intersection coordinates are exact rationals", () => {
    const g = grid(2, 2, [
      [0, 3],
      [0, 0],
    ]);
    const r = extractContours(g, 1);
    expect(r.segments.length).toBe(1);
    const pts = r.contours[0].points.map(key).sort();
    expect(pts).toEqual(["1,2/3", "1/3,0"]);
    const p = r.points.find((q) => q.x.toString() === "1/3")!;
    expect(p.x.num).toBe(1n);
    expect(p.x.den).toBe(3n);
    assertWellFormed(r, 2, 2);
  });

  test("vertex exactly at threshold: no dangling or duplicated segments", () => {
    const g = grid(3, 3, [
      [0, 2, 0],
      [0, 1, 0],
      [0, 2, 0],
    ]);
    const r = extractContours(g, 1);
    // The at-threshold vertex (1,1) is a single shared point of degree 4.
    const centerCount = r.points.filter((p) => key(p) === "1,1").length;
    expect(centerCount).toBe(1);
    expect(r.points.length).toBe(5);
    expect(r.segments.length).toBe(4);
    expect(r.stats.degenerateSegmentsDropped).toBe(0);
    // Stitches into two open chains, both passing through (1,1).
    expect(r.contours.length).toBe(2);
    for (const c of r.contours) {
      expect(c.closed).toBe(false);
      expect(c.points.length).toBe(3);
      expect(key(c.points[1])).toBe("1,1");
    }
    assertWellFormed(r, 3, 3);
  });

  test("at-threshold vertex with adjacent above neighbours drops zero-length segment", () => {
    const g = grid(3, 3, [
      [0, 2, 0],
      [2, 1, 0],
      [0, 0, 0],
    ]);
    const r = extractContours(g, 1);
    // Cell (0,0) is a saddle whose R-B pair collapses onto vertex (1,1).
    expect(r.stats.degenerateSegmentsDropped).toBe(1);
    expect(r.segments.length).toBe(3);
    expect(r.contours.length).toBe(2);
    expect(r.contours.every((c) => !c.closed)).toBe(true);
    assertWellFormed(r, 3, 3);
  });

  test("flat edge at threshold: no crossings on it, no duplicated segments", () => {
    const g = grid(3, 3, [
      [2, 2, 2],
      [1, 1, 1],
      [0, 0, 0],
    ]);
    const r = extractContours(g, 1);
    // Contour runs exactly along the at-threshold row of vertices.
    expect(new Set(r.points.map(key))).toEqual(new Set(["0,1", "1,1", "2,1"]));
    expect(r.segments.length).toBe(2);
    expect(r.contours.length).toBe(1);
    const chain = r.contours[0];
    expect(chain.closed).toBe(false);
    expect(chain.points.map(key)).toEqual(["0,1", "1,1", "2,1"]);
    assertWellFormed(r, 3, 3);
  });

  test("flat plateau fully at threshold produces no contour", () => {
    const g = grid(3, 3, [
      [1, 1, 1],
      [1, 1, 1],
      [1, 1, 1],
    ]);
    const r = extractContours(g, 1);
    expect(r.points.length).toBe(0);
    expect(r.segments.length).toBe(0);
    expect(r.contours.length).toBe(0);
  });

  test("flat edge shared by two above regions: coincident contour emitted once", () => {
    const g = grid(3, 3, [
      [0, 2, 2],
      [0, 1, 1],
      [0, 2, 2],
    ]);
    const r = extractContours(g, 1);
    // Cells above and below the flat edge (1,1)-(2,1) both produce the
    // segment along it; exactly one copy must survive.
    expect(r.stats.duplicateSegmentsDropped).toBe(1);
    expect(r.segments.length).toBe(3);
    const keys = segmentKeys(r);
    expect(new Set(keys).size).toBe(keys.length);
    assertWellFormed(r, 3, 3);
  });

  test("saddle: bilinear center above threshold connects the above corners", () => {
    const g = grid(2, 2, [
      [0, 5],
      [5, 0],
    ]);
    const r = extractContours(g, 2);
    expect(segmentKeys(r).sort()).toEqual(
      ["0,2/5|2/5,0", "1,3/5|3/5,1"].sort(),
    );
    assertWellFormed(r, 2, 2);
  });

  test("saddle: bilinear center below threshold connects the below corners", () => {
    const g = grid(2, 2, [
      [0, 3],
      [3, 0],
    ]);
    const r = extractContours(g, 2);
    expect(segmentKeys(r).sort()).toEqual(
      ["1,1/3|2/3,0", "0,2/3|1/3,1"].sort(),
    );
    assertWellFormed(r, 2, 2);
  });

  test("saddle tie: center equal to threshold uses the fixed TL-BR diagonal", () => {
    // Case 5 (TR+BL above), center sum 12 == 4 * 3.
    const g5 = grid(2, 2, [
      [0, 6],
      [6, 0],
    ]);
    const r5 = extractContours(g5, 3);
    const expected = ["1,1/2|1/2,0", "0,1/2|1/2,1"].sort();
    expect(segmentKeys(r5).sort()).toEqual(expected);

    // Case 10 (TL+BR above) is the complement: fixed diagonal must give
    // identical geometry so complementary cells stay consistent.
    const g10 = grid(2, 2, [
      [6, 0],
      [0, 6],
    ]);
    const r10 = extractContours(g10, 3);
    expect(segmentKeys(r10).sort()).toEqual(expected);
    assertWellFormed(r5, 2, 2);
    assertWellFormed(r10, 2, 2);
  });

  test("rational threshold participates in exact saddle comparison", () => {
    // Center sum 10 -> center value 5/2; threshold 5/2 is an exact tie.
    const g = grid(2, 2, [
      [0, 5],
      [5, 0],
    ]);
    const tie = extractContours(g, Rational.ratio(5n, 2n));
    expect(segmentKeys(tie).sort()).toEqual(
      ["1,1/2|1/2,0", "0,1/2|1/2,1"].sort(),
    );
    // Slightly below the tie flips the connection.
    const below = extractContours(g, Rational.ratio(2n, 1n));
    expect(segmentKeys(below).sort()).toEqual(
      ["0,2/5|2/5,0", "1,3/5|3/5,1"].sort(),
    );
  });

  test("closed loop plus open chain in one grid", () => {
    const g = grid(4, 3, [
      [0, 0, 0, 5],
      [0, 9, 0, 5],
      [0, 0, 0, 5],
    ]);
    const r = extractContours(g, 2);
    const loops = r.contours.filter((c) => c.closed);
    const chains = r.contours.filter((c) => !c.closed);
    expect(loops.length).toBe(1);
    expect(chains.length).toBe(1);
    expect(chains[0].points.map(key)).toEqual(["12/5,0", "12/5,1", "12/5,2"]);
    assertWellFormed(r, 4, 3);
  });

  test("randomized grids keep structural invariants (no exact threshold hits)", () => {
    let seed = 0xC0FFEE;
    const rand = (): number => {
      seed |= 0;
      seed = (seed + 0x6D2B79F5) | 0;
      let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
    for (let iter = 0; iter < 300; iter++) {
      const w = 2 + Math.floor(rand() * 5);
      const h = 2 + Math.floor(rand() * 5);
      const data = Array.from({ length: w * h }, () => Math.floor(rand() * 4));
      const r = extractContours({ width: w, height: h, data }, Rational.ratio(3n, 2n));
      assertWellFormed(r, w, h, { expectBoundaryChainEnds: true });
    }
  });

  test("randomized grids keep structural invariants (exact threshold hits)", () => {
    let seed = 0xBADC0DE;
    const rand = (): number => {
      seed |= 0;
      seed = (seed + 0x6D2B79F5) | 0;
      let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
    for (let iter = 0; iter < 300; iter++) {
      const w = 2 + Math.floor(rand() * 5);
      const h = 2 + Math.floor(rand() * 5);
      const data = Array.from({ length: w * h }, () => Math.floor(rand() * 4));
      const threshold = 1 + Math.floor(rand() * 2); // 1..2, hits exact values
      const r = extractContours({ width: w, height: h, data }, threshold);
      assertWellFormed(r, w, h);
    }
  });

  test("rejects invalid input", () => {
    expect(() => extractContours({ width: 2, height: 2, data: [1, 2, 3] }, 1)).toThrow();
    expect(() =>
      extractContours({ width: 1, height: 1, data: [1.5] }, 1),
    ).toThrow();
  });
});
