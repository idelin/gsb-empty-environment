import { strict as assert } from "assert";
import { Fraction } from "../src/fraction";
import {
  fitSegmentedRegression,
  FitResult,
  IntInput,
  SingularSegmentError,
} from "../src/segmented-regression";

let passed = 0;
function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`ok - ${name}`);
  } catch (err) {
    console.error(`FAIL - ${name}`);
    console.error(err);
    process.exitCode = 1;
  }
}

// ---------- Fraction ----------

test("fraction normalizes sign and gcd", () => {
  const f = Fraction.fromRatio(6n, -9n);
  assert.equal(f.num, -2n);
  assert.equal(f.den, 3n);
  assert.equal(Fraction.fromRatio(0n, -5n).toString(), "0");
});

test("fraction arithmetic is exact", () => {
  const a = Fraction.parse("1/3");
  const b = Fraction.parse("1/6");
  assert.equal(a.add(b).toString(), "1/2");
  assert.equal(a.sub(b).toString(), "1/6");
  assert.equal(a.mul(b).toString(), "1/18");
  assert.equal(a.div(b).toString(), "2");
  assert.equal(a.neg().toString(), "-1/3");
});

test("fraction comparison distinguishes values float64 cannot", () => {
  // Both sides round to the same double (1), but differ exactly.
  const big = 9007199254740992n; // 2^53
  const f = Fraction.fromRatio(big + 1n, big);
  assert.equal(f.toNumber(), 1);
  assert.equal(f.cmp(Fraction.of(1n)), 1);
  assert.equal(Fraction.of(1n).cmp(f), -1);
});

test("fraction division by zero throws instead of producing garbage", () => {
  assert.throws(() => Fraction.parse("1/0"), RangeError);
  assert.throws(() => Fraction.of(1n).div(Fraction.of(0n)), RangeError);
});

// ---------- Basic fitting ----------

test("recovers two exact lines with zero error", () => {
  const xs = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9];
  const ys = [0, 2, 4, 6, 8, 5, 4, 3, 2, 1]; // y=2x then y=10-x
  const r = fitSegmentedRegression(xs, ys, { maxSegments: 3 });
  assert.equal(r.segmentCount, 2);
  assert.deepEqual(r.breakpoints, [5]);
  assert.equal(r.totalSse.toString(), "0");
  assert.equal(r.segments[0].slope.toString(), "2");
  assert.equal(r.segments[0].intercept.toString(), "0");
  assert.equal(r.segments[1].slope.toString(), "-1");
  assert.equal(r.segments[1].intercept.toString(), "10");
});

test("equal error prefers fewer segments", () => {
  // Perfectly collinear: any extra split also has SSE 0, must pick 1 segment.
  const xs = [0, 1, 2, 3, 4, 5, 6, 7, 8];
  const ys = xs.map((x) => 3 * x - 5);
  const r = fitSegmentedRegression(xs, ys, { maxSegments: 3 });
  assert.equal(r.segmentCount, 1);
  assert.deepEqual(r.breakpoints, []);
  assert.equal(r.totalSse.toString(), "0");
  assert.equal(r.segments[0].slope.toString(), "3");
  assert.equal(r.segments[0].intercept.toString(), "-5");
});

test("every segment has at least minSegmentLength points", () => {
  const xs = Array.from({ length: 12 }, (_, i) => i);
  const ys = [0, 0, 0, 1, 2, 3, 10, 10, 10, 5, 3, 1];
  const r = fitSegmentedRegression(xs, ys, { maxSegments: 4, minSegmentLength: 3 });
  for (const seg of r.segments) {
    assert.ok(seg.count >= 3, `segment length ${seg.count} < 3`);
  }
});

test("maxSegments is clamped to what minSegmentLength allows", () => {
  // 5 points cannot hold two 3-point segments; maxSegments=2 clamps to 1.
  const r = fitSegmentedRegression([0, 1, 2, 3, 4], [0, 1, 0, 1, 0], { maxSegments: 2 });
  assert.equal(r.segmentCount, 1);
});

test("too few points for one segment throws RangeError", () => {
  assert.throws(
    () => fitSegmentedRegression([0, 1], [0, 1], { maxSegments: 1 }),
    RangeError
  );
  assert.throws(
    () => fitSegmentedRegression([], [], { maxSegments: 1 }),
    RangeError
  );
});

test("non-integer input is rejected", () => {
  assert.throws(
    () => fitSegmentedRegression([0, 1.5, 2], [0, 1, 2], { maxSegments: 1 }),
    TypeError
  );
});

// ---------- Singular segments (repeated x) ----------

test("singular segment: constant mode fits mean and flags it", () => {
  // One singular segment: all x equal to 5.
  const r = fitSegmentedRegression([5, 5, 5, 5], [1, 2, 3, 4], { maxSegments: 1 });
  assert.equal(r.segmentCount, 1);
  const seg = r.segments[0];
  assert.equal(seg.singular, true);
  assert.equal(seg.slope.toString(), "0");
  assert.equal(seg.intercept.toString(), "5/2");
  // SSE = sum(y^2) - sum(y)^2/n = 30 - 100/4 = 5.
  assert.equal(seg.sse.toString(), "5");
  assert.equal(r.totalSse.toString(), "5");
});

test("singular segment: constant fit is the exact SSE minimizer", () => {
  // y = [2, 4, 9] at x = 3: mean is 5, SSE = 9+1+16 = 26.
  const r = fitSegmentedRegression([3, 3, 3], [2, 4, 9], { maxSegments: 1 });
  assert.equal(r.segments[0].intercept.toString(), "5");
  assert.equal(r.totalSse.toString(), "26");
});

test("singular segment: error mode throws SingularSegmentError, not a raw div-by-zero", () => {
  assert.throws(
    () =>
      fitSegmentedRegression([7, 7, 7], [1, 2, 3], {
        maxSegments: 1,
        singular: "error",
      }),
    (err: unknown) => err instanceof SingularSegmentError
  );
});

test("singular segment: reject mode avoids or reports infeasible", () => {
  // Unavoidable singular segment -> infeasible.
  assert.throws(
    () =>
      fitSegmentedRegression([4, 4, 4], [1, 2, 3], {
        maxSegments: 1,
        singular: "reject",
      }),
    RangeError
  );
  // Avoidable: a clean two-line dataset where no singular segment is needed.
  const xs = [0, 1, 2, 3, 4, 5];
  const ys = [0, 1, 2, 9, 8, 7];
  const r = fitSegmentedRegression(xs, ys, { maxSegments: 2, singular: "reject" });
  assert.ok(r.segments.every((s) => !s.singular));
  assert.equal(r.totalSse.toString(), "0");
});

test("repeated x inside a longer segment is handled exactly", () => {
  // x has repeats but is not constant, so the segment is NOT singular.
  const xs = [0, 1, 1, 2, 3, 3];
  const ys = [1, 2, 3, 4, 5, 6];
  const r = fitSegmentedRegression(xs, ys, { maxSegments: 1, minSegmentLength: 3 });
  assert.equal(r.segments[0].singular, false);
  // Cross-check SSE against a direct rational computation.
  const direct = directSse(xs.map(BigInt), ys.map(BigInt));
  assert.equal(r.totalSse.toString(), direct.sse.toString());
});

// ---------- Exactness with huge integers ----------

test("exact fit beyond float64 precision (coordinates near 2^60)", () => {
  const base = 1n << 60n;
  const xs = [0n, 1n, 2n, 3n, 4n, 5n].map((d) => (base + d).toString());
  const ys = [0n, 1n, 2n, 3n, 4n, 5n].map((d) => 3n * (base + d) + 2n);
  const r = fitSegmentedRegression(xs, ys.map(String), { maxSegments: 2 });
  assert.equal(r.segmentCount, 1);
  assert.equal(r.segments[0].slope.toString(), "3");
  assert.equal(r.segments[0].intercept.toString(), "2");
  assert.equal(r.totalSse.toString(), "0");
});

// ---------- Brute-force cross-validation ----------

/** Independent direct (non-prefix-sum) exact segment fit. */
function directSse(xs: bigint[], ys: bigint[]): { sse: Fraction } {
  const n = BigInt(xs.length);
  let sx = 0n, sy = 0n, sxy = 0n, sxx = 0n, syy = 0n;
  for (let i = 0; i < xs.length; i++) {
    sx += xs[i]; sy += ys[i];
    sxy += xs[i] * ys[i]; sxx += xs[i] * xs[i]; syy += ys[i] * ys[i];
  }
  const denom = n * sxx - sx * sx;
  if (denom === 0n) {
    return { sse: Fraction.fromRatio(syy * n - sy * sy, n) };
  }
  const b = n * sxy - sx * sy;
  return { sse: Fraction.fromRatio(syy * n * denom - sy * sy * denom - b * b, n * denom) };
}

/** Matches the DP tie-break: compare breakpoint vectors from last to first. */
function reverseLexCmp(a: number[], b: number[]): number {
  for (let k = 1; k <= Math.max(a.length, b.length); k++) {
    const x = a[a.length - k];
    const y = b[b.length - k];
    if (x === undefined && y === undefined) return 0;
    if (x === undefined) return -1;
    if (y === undefined) return 1;
    if (x !== y) return x < y ? -1 : 1;
  }
  return 0;
}

function bruteForce(
  xs: IntInput[],
  ys: IntInput[],
  maxSegments: number,
  minLen: number
): { sse: Fraction; segments: number; breakpoints: number[] } {
  const bx = xs.map((v) => BigInt(v));
  const by = ys.map((v) => BigInt(v));
  const n = bx.length;
  let best: { sse: Fraction; segments: number; breakpoints: number[] } | null = null;

  function consider(sse: Fraction, segments: number, breakpoints: number[]): void {
    if (best === null) {
      best = { sse, segments, breakpoints };
      return;
    }
    const c = sse.cmp(best.sse);
    if (c < 0 ||
        (c === 0 && segments < best.segments) ||
        (c === 0 && segments === best.segments &&
         reverseLexCmp(breakpoints, best.breakpoints) < 0)) {
      best = { sse, segments, breakpoints };
    }
  }

  function recurse(start: number, segmentsUsed: number, sse: Fraction, bps: number[]): void {
    const remaining = n - start;
    // Option 1: finish here with one final segment.
    if (remaining >= minLen && segmentsUsed + 1 <= maxSegments) {
      consider(sse.add(directSse(bx.slice(start), by.slice(start)).sse), segmentsUsed + 1, bps);
    }
    // Option 2: place a breakpoint; at least one more segment must follow.
    if (segmentsUsed + 2 <= maxSegments) {
      for (let end = start + minLen; end <= n - minLen; end++) {
        recurse(end, segmentsUsed + 1, sse.add(directSse(bx.slice(start, end), by.slice(start, end)).sse), [...bps, end]);
      }
    }
  }
  recurse(0, 0, Fraction.of(0n), []);
  if (best === null) throw new Error("brute force found no partition");
  return best!;
}

function lcg(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0;
    return state;
  };
}

test("DP matches brute-force enumeration on random inputs", () => {
  const rand = lcg(42);
  for (let trial = 0; trial < 200; trial++) {
    const n = 3 + (rand() % 8); // 3..10 points
    const xs: number[] = [];
    const ys: number[] = [];
    for (let i = 0; i < n; i++) {
      // Deliberately allow repeated x to exercise singular segments.
      xs.push(rand() % 4);
      ys.push((rand() % 11) - 5);
    }
    const maxSegments = 1 + (rand() % 3);
    const r: FitResult = fitSegmentedRegression(xs, ys, { maxSegments });
    const bf = bruteForce(xs, ys, maxSegments, 3);
    assert.equal(
      r.totalSse.toString(),
      bf.sse.toString(),
      `trial ${trial}: sse ${r.totalSse} != brute ${bf.sse} (xs=${xs} ys=${ys} K=${maxSegments})`
    );
    assert.equal(r.segmentCount, bf.segments, `trial ${trial}: segment count`);
    assert.deepEqual(r.breakpoints, bf.breakpoints, `trial ${trial}: breakpoints`);
  }
});

test("DP matches brute force with larger coordinates and no repeated-x crutch", () => {
  const rand = lcg(7);
  for (let trial = 0; trial < 100; trial++) {
    const n = 6 + (rand() % 5);
    const xs = Array.from({ length: n }, (_, i) => i * 2);
    const ys = Array.from({ length: n }, () => (rand() % 2001) - 1000);
    const maxSegments = 1 + (rand() % 3);
    const r = fitSegmentedRegression(xs, ys, { maxSegments });
    const bf = bruteForce(xs, ys, maxSegments, 3);
    assert.equal(r.totalSse.toString(), bf.sse.toString(), `trial ${trial}`);
    assert.deepEqual(r.breakpoints, bf.breakpoints, `trial ${trial}`);
  }
});

console.log(`\n${passed} tests passed${process.exitCode ? " (with failures)" : ""}`);
