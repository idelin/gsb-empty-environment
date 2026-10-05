import { GainInterpolator } from "../src/index";

const gi = new GainInterpolator({ speakers: 4, fracBits: 32 });

// Integer-weight control points; the second point at t=48000 overwrites.
gi.addControlPoint(0, [10, 0, 0, 0]);
gi.addControlPoint(48000, [7, 7, 0, 0]);
gi.addControlPoint(48000, [6, 8, 0, 0]);
gi.addControlPoint(96000, [0, 0, 0, 0]); // fade to silence

// Static feasibility of the configuration.
const check = GainInterpolator.checkConfig({
  speakers: 4,
  fracBits: 32,
  energyErrorBound: 1e-6,
});
console.log("config feasible:", check.feasible, "(needs", check.requiredFracBits, "bits)");

// Process in blocks; identical to one-shot processing.
const block = gi.processBlock(47996, 8);
for (const f of block) {
  const gains = f.gains.map((g) => (Number(g) / 2 ** 32).toFixed(6));
  console.log(
    `t=${f.time} muted=${f.muted} gains=[${gains}] ` +
      `energyErr<=${gi.energyToNumber(f.energyErrorBound).toExponential(2)}`,
  );
}

// Verify a whole range against an error bound.
const report = gi.analyze(0, 96000, 1e-6);
console.log("range feasible:", report.feasible,
  "max certified error:", report.maxEnergyErrorBound.toExponential(2));
for (const reason of report.reasons) console.log("reason:", reason);
