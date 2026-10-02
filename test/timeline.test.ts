import { test } from "node:test";
import assert from "node:assert/strict";
import { compileRule } from "../src/timecode.ts";
import { Timeline } from "../src/timeline.ts";
import { abs, cmp, rat, sub, toString } from "../src/rational.ts";

// Two rule "versions": NTSC 29.97 DF and film 23.976 NDF.
const df = compileRule(
  "ntsc-29.97-df",
  { nominalFps: 30, periodSeconds: 60, dropFrameLabels: [0, 1], keepEveryPeriods: 10 },
  rat(30000n, 1001n),
);
const ndf24 = compileRule("film-23.976-ndf", { nominalFps: 24, periodSeconds: 60, dropFrameLabels: [] }, rat(24000n, 1001n));

const tl = new Timeline([
  { rule: df, sourceStartFrame: 0n, frames: 1800n }, // 1800 * 1001/30000 = 3003/50 s
  { rule: ndf24, sourceStartFrame: 100n, frames: 240n }, // 240 * 1001/24000 = 1001/100 s
]);

test("splice layout across rule versions", () => {
  assert.equal(tl.totalFrames, 2040n);
  assert.deepEqual(tl.locate(0n), { segmentIndex: 0, sourceFrame: 0n, offsetInSegment: 0n });
  assert.deepEqual(tl.locate(1799n), { segmentIndex: 0, sourceFrame: 1799n, offsetInSegment: 1799n });
  assert.deepEqual(tl.locate(1800n), { segmentIndex: 1, sourceFrame: 100n, offsetInSegment: 0n });
  assert.deepEqual(tl.locate(2039n), { segmentIndex: 1, sourceFrame: 339n, offsetInSegment: 239n });
  assert.throws(() => tl.locate(2040n), RangeError);
  assert.throws(() => tl.locate(-1n), RangeError);
});

test("cut times are exact rational sums, no float accumulation", () => {
  assert.equal(toString(tl.cutTime(0n)), "0");
  assert.equal(toString(tl.cutTime(1n)), "1001/30000");
  assert.equal(toString(tl.cutTime(1800n)), "3003/50"); // 60.06 s exactly
  assert.equal(toString(tl.cutTime(2040n)), "7007/100"); // 3003/50 + 1001/100 = 70.07 s
});

test("audio sample alignment reports exact rational error", () => {
  // Boundary 1 at 48 kHz: time 1001/30000 s -> 1601.6 samples -> half-up 1602.
  const cut = tl.audioCut(1n, 48000n, "half-up");
  assert.equal(cut.sample, 1602n);
  assert.equal(toString(cut.error), "1/120000"); // 1602/48000 - 1001/30000, exact
  // Boundary 1800 lands exactly on a sample: 3003/50 * 48000 = 2882880.
  const exact = tl.audioCut(1800n, 48000n, "half-up");
  assert.equal(exact.sample, 2882880n);
  assert.equal(toString(exact.error), "0");
  // floor mode always errs non-positive, within one sample period.
  const floored = tl.audioCut(1n, 48000n, "floor");
  assert.equal(floored.sample, 1601n);
  assert.equal(toString(floored.error), "-1/80000"); // 1601/48000 - 1001/30000, exact
});

test("alignment error is bounded by half a sample for half-up, one sample for floor", () => {
  const halfSample = rat(1n, 2n * 48000n);
  const oneSample = rat(1n, 48000n);
  for (let boundary = 0n; boundary <= tl.totalFrames; boundary += 7n) {
    const up = tl.audioCut(boundary, 48000n, "half-up");
    assert.ok(cmp(abs(up.error), halfSample) <= 0, `half-up boundary ${boundary}`);
    const fl = tl.audioCut(boundary, 48000n, "floor");
    assert.ok(cmp(abs(fl.error), oneSample) < 0, `floor boundary ${boundary}`);
    // error consistency: error == sample/rate - time, recomputed independently
    assert.equal(toString(up.error), toString(sub(rat(up.sample, 48000n), up.time)));
  }
});

test("cross-rule cut inside second segment stays exact", () => {
  // 1800 DF frames + 100 NDF24 frames: 3003/50 + 100*1001/24000
  const t = tl.cutTime(1900n);
  assert.equal(toString(t), "77077/1200"); // 64.2308333... s, exact
  const cut = tl.audioCut(1900n, 48000n, "half-up");
  assert.equal(cut.sample, 3083080n); // 77077/1200 * 48000 = 3083080 exactly
  assert.equal(toString(cut.error), "0");
});
