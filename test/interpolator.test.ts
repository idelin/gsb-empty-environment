import { describe, expect, test } from "bun:test";
import { GainInterpolator, Frame } from "../src/index";

const F = 32;
const SCALE = 2 ** F;

function gainsAsNumbers(frame: Frame): number[] {
  return frame.gains.map((g) => Number(g) / SCALE);
}

function energy(frame: Frame): number {
  return gainsAsNumbers(frame).reduce((acc, g) => acc + g * g, 0);
}

function makeInterpolator(points: Array<[number, number[]]>, speakers = 2) {
  const gi = new GainInterpolator({ speakers, fracBits: F });
  for (const [t, w] of points) gi.addControlPoint(t, w);
  return gi;
}

describe("interpolation", () => {
  test("linear interpolation between control points", () => {
    const gi = makeInterpolator([[0, [10, 0]], [10, [10, 10]]]);
    const g = gainsAsNumbers(gi.frameAt(5));
    // weights (10, 5) normalised: (2/sqrt5, 1/sqrt5)
    expect(g[0]).toBeCloseTo(2 / Math.sqrt(5), 6);
    expect(g[1]).toBeCloseTo(1 / Math.sqrt(5), 6);
  });

  test("holds first/last control point outside the range", () => {
    const gi = makeInterpolator([[10, [3, 4]], [20, [4, 3]]]);
    expect(gainsAsNumbers(gi.frameAt(0))[0]).toBeCloseTo(0.6, 6);
    expect(gainsAsNumbers(gi.frameAt(100))[0]).toBeCloseTo(0.8, 6);
  });

  test("same-time control points overwrite earlier ones", () => {
    const gi = makeInterpolator([[0, [1, 0]], [100, [1, 0]]]);
    gi.addControlPoint(50, [1, 0]);
    gi.addControlPoint(50, [0, 2]); // overwrites
    const g = gainsAsNumbers(gi.frameAt(50));
    expect(g[0]).toBeCloseTo(0, 9);
    expect(Math.abs(g[1])).toBeCloseTo(1, 6);
    expect(gi.controlPoints().filter((p) => p.time === 50)).toHaveLength(1);
  });

  test("rejects non-integer weights and wrong arity", () => {
    const gi = new GainInterpolator({ speakers: 2 });
    expect(() => gi.addControlPoint(0, [1.5, 0])).toThrow(RangeError);
    expect(() => gi.addControlPoint(0, [1, 2, 3])).toThrow(RangeError);
    expect(() => gi.addControlPoint(-1, [1, 2])).toThrow(RangeError);
  });
});

describe("normalisation and certified energy-error enclosure", () => {
  test("energy stays within the certified bound, which meets the limit", () => {
    const gi = makeInterpolator(
      [[0, [7, 1]], [333, [2, 9]], [1000, [5, 5]]],
    );
    const limit = 1e-6;
    const report = gi.analyze(0, 1000, limit);
    expect(report.feasible).toBe(true);
    expect(report.violations).toHaveLength(0);
    // The certified bound really encloses the actual error.
    for (const frame of gi.processBlock(0, 1000)) {
      if (frame.muted) continue;
      const actual = Math.abs(energy(frame) - 1);
      const bound = gi.energyToNumber(frame.energyErrorBound);
      expect(actual).toBeLessThanOrEqual(bound);
      expect(bound).toBeLessThanOrEqual(limit);
    }
  });

  test("zero weight vector is emitted as silence", () => {
    const gi = makeInterpolator([[0, [0, 0]], [10, [0, 0]]]);
    const frame = gi.frameAt(5);
    expect(frame.muted).toBe(true);
    expect(frame.gains).toEqual([0n, 0n]);
    expect(frame.energyErrorBound).toBe(0n);
    expect(energy(frame)).toBe(0);
  });

  test("fade from silence: exact-zero sample muted, neighbours normalised", () => {
    const gi = makeInterpolator([[0, [0, 0]], [100, [8, 0]]]);
    expect(gi.frameAt(0).muted).toBe(true);
    const later = gi.frameAt(50);
    expect(later.muted).toBe(false);
    expect(Math.abs(energy(later) - 1)).toBeLessThan(1e-6);
  });
});

describe("block processing equals one-shot processing", () => {
  test("any chunk partitioning is bit-identical", () => {
    const gi = makeInterpolator(
      [[0, [9, 1, 2]], [137, [3, 8, 4]], [512, [0, 0, 0]], [900, [5, 5, 5]]],
      3,
    );
    const N = 900;
    const oneShot = gi.processBlock(0, N);
    for (const chunk of [1, 2, 7, 64, 255, N]) {
      const parts: Frame[] = [];
      for (let start = 0; start < N; start += chunk) {
        parts.push(...gi.processBlock(start, Math.min(chunk, N - start)));
      }
      expect(parts).toEqual(oneShot);
    }
  });
});

describe("feasibility reporting", () => {
  test("checkConfig reports configurations that cannot reach the precision", () => {
    const ok = GainInterpolator.checkConfig({
      speakers: 8,
      fracBits: 32,
      energyErrorBound: 1e-6,
    });
    expect(ok.feasible).toBe(true);

    const bad = GainInterpolator.checkConfig({
      speakers: 8,
      fracBits: 8,
      energyErrorBound: 1e-9,
    });
    expect(bad.feasible).toBe(false);
    expect(bad.requiredFracBits).toBeGreaterThan(8);
    expect(bad.reasons[0]).toContain("insufficient");
  });

  test("analyze flags a too-tight bound for the chosen fracBits", () => {
    const gi = new GainInterpolator({ speakers: 4, fracBits: 16 });
    gi.addControlPoint(0, [3, 1, 4, 1]);
    gi.addControlPoint(500, [5, 9, 2, 6]);
    const report = gi.analyze(0, 500, 1e-12);
    expect(report.feasible).toBe(false);
    expect(report.violations.length).toBeGreaterThan(0);
    expect(report.maxEnergyErrorBound).toBeGreaterThan(1e-12);
    expect(report.reasons[0]).toContain("fracBits");
  });

  test("requiredFracBits matches the steady-state error model", () => {
    const f = GainInterpolator.requiredFracBits(8, 1e-6);
    expect(2 * Math.sqrt(8) / 2 ** f).toBeLessThanOrEqual(1e-6);
    expect(2 * Math.sqrt(8) / 2 ** (f - 1)).toBeGreaterThan(1e-6);
  });
});
