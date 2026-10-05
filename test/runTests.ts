/**
 * Dependency-free test suite. Compile with tsc, then run:
 *   node dist/test/runTests.js
 */
import {
  GainInterpolator,
  InterpolatorConfig,
  analyzeConfiguration,
  measureEnergyError,
  isqrt,
  roundDiv,
} from "../src/index";

let failures = 0;
let checks = 0;

function check(cond: boolean, message: string): void {
  checks++;
  if (!cond) {
    failures++;
    console.error(`FAIL: ${message}`);
  }
}

function section(name: string): void {
  console.log(`--- ${name}`);
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

const BASE_CONFIG: InterpolatorConfig = {
  numSpeakers: 4,
  maxWeight: 1000,
  fracBits: 24,
  outFracBits: 20,
  guardBits: 16,
};

// ---------------------------------------------------------------------------
section("fixedPoint kernel");
{
  check(isqrt(0n) === 0n, "isqrt(0)");
  check(isqrt(1n) === 1n, "isqrt(1)");
  check(isqrt(2n) === 1n, "isqrt(2)");
  check(isqrt(4n) === 2n, "isqrt(4)");
  check(isqrt(10n ** 30n) === 10n ** 15n, "isqrt(1e30)");
  const rnd = mulberry32(42);
  let ok = true;
  for (let i = 0; i < 500; i++) {
    const n = BigInt(Math.floor(rnd() * 1e12));
    const r = isqrt(n);
    if (r * r > n || (r + 1n) * (r + 1n) <= n) ok = false;
  }
  check(ok, "isqrt floor property over 500 random values");
  check(roundDiv(7n, 2n) === 4n, "roundDiv(7,2) rounds half away from zero");
  check(roundDiv(6n, 4n) === 2n, "roundDiv(6,4)");
  check(roundDiv(5n, 4n) === 1n, "roundDiv(5,4)");
  check(roundDiv(-7n, 2n) === -4n, "roundDiv negative");
}

// ---------------------------------------------------------------------------
section("interpolation and normalization");
{
  const interp = new GainInterpolator(BASE_CONFIG);
  interp.addControlPoint(0, [1000, 0, 0, 0]);
  interp.addControlPoint(10, [0, 1000, 0, 0]);
  const frames = interp.process(0, 11);
  const mid = frames[5];
  // Midpoint: both speakers at equal weight -> each gain = 1/sqrt(2).
  const expected = Math.SQRT1_2;
  const got0 = mid[0] / 2 ** BASE_CONFIG.outFracBits;
  const got1 = mid[1] / 2 ** BASE_CONFIG.outFracBits;
  check(Math.abs(got0 - expected) < 1e-4, `midpoint gain speaker 0 (${got0})`);
  check(Math.abs(got1 - expected) < 1e-4, `midpoint gain speaker 1 (${got1})`);
  check(mid[2] === 0 && mid[3] === 0, "inactive speakers stay zero");
  // Energy of every non-silent frame is close to 1.
  let maxErr = 0;
  for (const f of frames) {
    maxErr = Math.max(maxErr, measureEnergyError(f, BASE_CONFIG.outFracBits));
  }
  check(maxErr < 1e-4, `energy error across segment (${maxErr})`);
  // Monotonic crossfade: speaker 0 decreases, speaker 1 increases.
  let mono = true;
  for (let i = 1; i < frames.length; i++) {
    if (frames[i][0] > frames[i - 1][0]) mono = false;
    if (frames[i][1] < frames[i - 1][1]) mono = false;
  }
  check(mono, "crossfade is monotonic");
}

// ---------------------------------------------------------------------------
section("zero-weight vectors are silence");
{
  const interp = new GainInterpolator(BASE_CONFIG);
  interp.addControlPoint(0, [0, 0, 0, 0]);
  interp.addControlPoint(8, [1000, 1000, 1000, 1000]);
  const frames = interp.process(0, 9);
  check(
    frames[0].every((g) => g === 0),
    "frame at zero-weight control point is exactly zero"
  );
  // Before the first control point: silence as well.
  const interp2 = new GainInterpolator(BASE_CONFIG);
  interp2.addControlPoint(5, [500, 0, 0, 0]);
  const early = interp2.process(0, 5);
  check(
    early.every((f) => f.every((g) => g === 0)),
    "samples before the first control point are silent"
  );
  // Interpolation from silence ramps up from zero without NaN/garbage.
  let ok = true;
  for (const f of frames) {
    for (const g of f) {
      if (!Number.isSafeInteger(g) || g < 0) ok = false;
    }
  }
  check(ok, "frames interpolating out of silence are valid non-negative integers");
}

// ---------------------------------------------------------------------------
section("same-time control point override");
{
  const interp = new GainInterpolator(BASE_CONFIG);
  interp.addControlPoint(0, [1000, 0, 0, 0]);
  interp.addControlPoint(10, [0, 1000, 0, 0]);
  interp.addControlPoint(10, [0, 0, 1000, 0]); // overrides the previous t=10
  const frames = interp.process(10, 1);
  check(frames[0][1] === 0, "overridden control point has no effect");
  check(
    frames[0][2] === 2 ** BASE_CONFIG.outFracBits,
    "latest control point at the same time wins"
  );
  // Hold: after the last control point the value persists.
  const held = interp.process(10, 50);
  check(
    held.every((f) => f[2] === 2 ** BASE_CONFIG.outFracBits),
    "last control point value is held"
  );
}

// ---------------------------------------------------------------------------
section("block processing equals one-shot processing");
{
  const rnd = mulberry32(1234);
  const config: InterpolatorConfig = {
    numSpeakers: 6,
    maxWeight: 4095,
    fracBits: 28,
    outFracBits: 24,
    guardBits: 20,
  };
  const total = 2000;
  const controlPoints: Array<[number, number[]]> = [];
  {
    let t = 0;
    while (t < total + 100) {
      const weights: number[] = [];
      for (let s = 0; s < config.numSpeakers; s++) {
        weights.push(Math.floor(rnd() * (config.maxWeight + 1)));
      }
      controlPoints.push([t, weights]);
      t += 1 + Math.floor(rnd() * 37);
    }
  }
  const makeInterp = (): GainInterpolator => {
    const it = new GainInterpolator(config);
    for (const [t, weights] of controlPoints) {
      it.addControlPoint(t, weights);
    }
    return it;
  };
  const reference = makeInterp().process(0, total);
  const blockSizes = [1, 7, 64, 333, 1000, total];
  for (const bs of blockSizes) {
    const it = makeInterp();
    const streamed: number[][] = [];
    while (streamed.length < total) {
      streamed.push(...it.processBlock(Math.min(bs, total - streamed.length)));
    }
    let identical = streamed.length === reference.length;
    for (let i = 0; identical && i < reference.length; i++) {
      for (let s = 0; s < config.numSpeakers; s++) {
        if (streamed[i][s] !== reference[i][s]) identical = false;
      }
    }
    check(identical, `block size ${bs} matches one-shot processing`);
  }
  // Interleaved: stream a bit, add more control points, stream the rest.
  const a = makeInterp();
  const first = a.processBlock(500);
  const rest = a.processBlock(total - 500);
  const combined = first.concat(rest);
  let identical = combined.length === reference.length;
  for (let i = 0; identical && i < reference.length; i++) {
    for (let s = 0; s < config.numSpeakers; s++) {
      if (combined[i][s] !== reference[i][s]) identical = false;
    }
  }
  check(identical, "split streaming session matches one-shot processing");
}

// ---------------------------------------------------------------------------
section("energy error stays within the analyzed bound");
{
  const config = {
    numSpeakers: 8,
    maxWeight: 4095,
    fracBits: 24,
    outFracBits: 20,
    guardBits: 16,
    minGain: 0.02,
  };
  const tolerance = 1e-4;
  const report = analyzeConfiguration(config, tolerance);
  check(report.feasible, `config should be feasible: ${report.reasons.join("; ")}`);
  check(
    report.energyErrorBound <= tolerance,
    "analyzed bound respects tolerance"
  );

  // Monte Carlo: measured error never exceeds the analytic bound.
  const rnd = mulberry32(777);
  const interp = new GainInterpolator(config);
  let t = 0;
  while (t < 4000) {
    const weights: number[] = [];
    for (let s = 0; s < config.numSpeakers; s++) {
      // Bias toward larger weights so frames stay above minGain.
      weights.push(Math.floor((0.2 + 0.8 * rnd()) * config.maxWeight));
    }
    interp.addControlPoint(t, weights);
    t += 1 + Math.floor(rnd() * 50);
  }
  const frames = interp.process(0, 4000);
  let maxMeasured = 0;
  let withinTolerance = true;
  for (const f of frames) {
    const err = measureEnergyError(f, config.outFracBits);
    maxMeasured = Math.max(maxMeasured, err);
    if (err > tolerance) withinTolerance = false;
  }
  check(withinTolerance, `measured energy error <= tolerance (max ${maxMeasured})`);
  check(
    maxMeasured <= report.energyErrorBound,
    `measured error (${maxMeasured}) <= analytic bound (${report.energyErrorBound})`
  );
}

// ---------------------------------------------------------------------------
section("infeasible configurations are reported");
{
  // Tiny output format cannot reach a tight tolerance.
  const coarse = analyzeConfiguration(
    { ...BASE_CONFIG, outFracBits: 4, minGain: 0.02 },
    1e-6
  );
  check(!coarse.feasible, "coarse output format reported infeasible");
  check(coarse.reasons.length > 0, "infeasible report carries reasons");
  check(coarse.suggestions.length > 0, "infeasible report carries suggestions");

  // minGain below one LSB of the gain format is vacuous.
  const vacuous = analyzeConfiguration(
    { ...BASE_CONFIG, fracBits: 8, minGain: 1e-6 },
    1
  );
  check(!vacuous.feasible, "sub-LSB minGain reported infeasible");

  // A generous configuration is feasible and reports small bounds.
  const good = analyzeConfiguration(
    {
      numSpeakers: 4,
      maxWeight: 1000,
      fracBits: 28,
      outFracBits: 24,
      guardBits: 24,
      minGain: 0.01,
    },
    1e-5
  );
  check(good.feasible, `generous config feasible: ${good.reasons.join("; ")}`);
  check(good.energyErrorBound <= 1e-5, "generous config meets tolerance");
  check(
    good.bitWidths.energy > 0 && good.bitWidths.rootOperand > good.bitWidths.energy,
    "bit-width report is populated"
  );
}

// ---------------------------------------------------------------------------
console.log(`\n${checks} checks, ${failures} failures`);
if (failures > 0) {
  process.exit(1);
}
console.log("all tests passed");
