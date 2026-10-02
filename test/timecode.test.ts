import { test } from "node:test";
import assert from "node:assert/strict";
import {
  compileRule,
  labelToFrame,
  frameToLabel,
  frameToTimecode,
  formatLabel,
  parseLabel,
  parseLabelOrNearest,
  frameToSample,
  sampleToFrame,
  frameToSampleExact,
  frameToSeconds,
  InvalidLabelError,
} from "../src/timecode.ts";
import { rat, toString } from "../src/rational.ts";

// NTSC 29.97 drop-frame, expressed as an explicit period table:
// drop labels ;00 and ;01 at the start of every minute, except every 10th minute.
const df = compileRule(
  "ntsc-29.97-df",
  { nominalFps: 30, periodSeconds: 60, dropFrameLabels: [0, 1], keepEveryPeriods: 10 },
  rat(30000n, 1001n),
);
const ndf24 = compileRule("film-23.976-ndf", { nominalFps: 24, periodSeconds: 60, dropFrameLabels: [] }, rat(24000n, 1001n));

test("compiled rank index: NTSC anchors", () => {
  assert.equal(df.validPerSuperPeriod, 17982n); // 10-minute super period
  assert.equal(frameToTimecode(df, 0n), "00:00:00;00");
  assert.equal(frameToTimecode(df, 1799n), "00:00:59;29");
  assert.equal(frameToTimecode(df, 1800n), "00:01:00;02"); // minute boundary skips ;00 ;01
  assert.equal(frameToTimecode(df, 17982n), "00:10:00;00"); // 10-minute exemption keeps labels
  assert.equal(labelToFrame(df, 1n, 0n, 0n, 0n), 107892n); // standard: 107892 frames per hour
  assert.equal(labelToFrame(df, 24n, 0n, 0n, 0n), 2589408n); // standard: 2589408 frames per day
});

test("minute boundary skip follows the drop table", () => {
  assert.equal(frameToTimecode(df, labelToFrame(df, 0n, 0n, 59n, 29n) + 1n), "00:01:00;02");
  assert.equal(frameToTimecode(df, labelToFrame(df, 0n, 9n, 59n, 29n) + 1n), "00:10:00;00");
  assert.equal(frameToTimecode(df, labelToFrame(df, 0n, 10n, 59n, 29n) + 1n), "00:11:00;02");
});

test("exhaustive round trip across period boundaries (labels)", () => {
  // Every second of minutes 0..12 (covers drop periods, the 10-minute
  // exemption, and the boundary on both sides), every frame label.
  for (let minute = 0; minute <= 12; minute++) {
    for (let s = 0; s < 60; s++) {
      for (let f = 0; f < 30; f++) {
        const h = 0n, m = BigInt(minute), ss = BigInt(s), ff = BigInt(f);
        const dropping = minute % 10 !== 0;
        if (dropping && s === 0 && (f === 0 || f === 1)) {
          assert.throws(() => labelToFrame(df, h, m, ss, ff), InvalidLabelError);
          continue;
        }
        const frame = labelToFrame(df, h, m, ss, ff);
        const back = frameToLabel(df, frame);
        assert.deepEqual(back, { h, m, s: ss, f: ff }, `label ${minute}:${s}:${f}`);
      }
    }
  }
});

test("exhaustive round trip across period boundaries (frames)", () => {
  // Frames 0..40000 cover two full 10-minute super periods (17982 each).
  for (let frame = 0n; frame < 40000n; frame++) {
    const l = frameToLabel(df, frame);
    assert.equal(labelToFrame(df, l.h, l.m, l.s, l.f), frame, `frame ${frame}`);
  }
});

test("long spans jump by period, not per-frame scan", () => {
  // 10^15 frames ~ 1056 years at 29.97: only computable via period jumps.
  for (const frame of [10n ** 15n, 10n ** 15n + 1n, 10n ** 15n + 17981n, 2n ** 60n + 12345n]) {
    const l = frameToLabel(df, frame);
    assert.equal(labelToFrame(df, l.h, l.m, l.s, l.f), frame);
  }
  // Consecutive frames stay consecutive across a super-period boundary far out.
  const base = (10n ** 12n / 17982n) * 17982n;
  const a = frameToLabel(df, base - 1n);
  const b = frameToLabel(df, base);
  assert.equal(labelToFrame(df, b.h, b.m, b.s, b.f) - labelToFrame(df, a.h, a.m, a.s, a.f), 1n);
});

test("nonexistent labels are rejected; nearest legal candidate returned", () => {
  assert.throws(() => parseLabel(df, "00:01:00;00"), (e) => e instanceof InvalidLabelError && e.reason === "dropped");
  assert.throws(() => parseLabel(df, "00:01:00;01"), InvalidLabelError);
  const fixed = parseLabelOrNearest(df, "00:01:00;00");
  assert.equal(fixed.corrected, true);
  assert.equal(fixed.label, "00:01:00;02");
  assert.equal(fixed.frame, 1800n);
  const ok = parseLabelOrNearest(df, "00:01:00;02");
  assert.equal(ok.corrected, false);
  assert.equal(ok.frame, 1800n);
});

test("separator, shape, range and negative-time policies", () => {
  assert.throws(() => parseLabel(df, "00:01:00:02"), (e) => e.reason === "bad-separator"); // DF needs ';'
  assert.throws(() => parseLabel(ndf24, "00:01:00;02"), (e) => e.reason === "bad-separator"); // NDF needs ':'
  assert.throws(() => parseLabel(df, "0:01:00;02"), (e) => e.reason === "bad-format");
  assert.throws(() => parseLabel(df, "00:01:00;2"), (e) => e.reason === "bad-format");
  assert.throws(() => parseLabel(df, "00:01:60;02"), (e) => e.reason === "bad-format");
  assert.throws(() => parseLabel(df, "-1:01:00;02"), (e) => e.reason === "bad-format");
  assert.throws(() => parseLabel(df, "00:01:00;30"), (e) => e.reason === "out-of-range"); // f >= nominalFps
  assert.throws(() => labelToFrame(df, -1n, 0n, 0n, 0n), (e) => e.reason === "negative");
  assert.throws(() => frameToLabel(df, -1n), RangeError);
});

test("label length policy: hours unbounded, ff width from nominalFps", () => {
  assert.equal(frameToTimecode(df, labelToFrame(df, 123n, 4n, 5n, 6n)), "123:04:05;06");
  const fps120 = compileRule("df-120", { nominalFps: 120, periodSeconds: 60, dropFrameLabels: [0, 1, 2, 3] }, rat(120n));
  assert.equal(fps120.ffWidth, 3);
  assert.equal(frameToTimecode(fps120, 0n), "00:00:00;004"); // no exemption: period 0 also drops ;000-;003
  assert.equal(parseLabel(fps120, "00:01:00;004"), 7196n); // 7200 - 4 valid labels in period 0
});

test("sample conversion is exact rational with explicit rounding", () => {
  // 29.97 (30000/1001) at 48 kHz: frame 1 = 48000*1001/30000 = 1601.6 samples
  assert.equal(toString(frameToSampleExact(df, 1n, 48000n)), "8008/5");
  assert.equal(frameToSample(df, 1n, 48000n, "floor"), 1601n);
  assert.equal(frameToSample(df, 1n, 48000n, "ceil"), 1602n);
  assert.equal(frameToSample(df, 1n, 48000n, "half-up"), 1602n);
  assert.equal(frameToSample(df, 1n, 48000n, "trunc"), 1601n);
  assert.equal(frameToSample(df, 0n, 48000n, "half-up"), 0n);
  // 23.976 at 44.1 kHz: frame 1 = 44100*1001/24000 = 1839.3375
  assert.equal(toString(frameToSampleExact(ndf24, 1n, 44100n)), "147147/80");
  assert.equal(frameToSample(ndf24, 1n, 44100n, "floor"), 1839n);
});

test("frame->sample->frame round trip is consistent (half-up both ways)", () => {
  for (let frame = 0n; frame < 20000n; frame++) {
    const sample = frameToSample(df, frame, 48000n, "half-up");
    assert.equal(sampleToFrame(df, sample, 48000n, "half-up"), frame, `frame ${frame}`);
  }
});

test("exact frame start times as rationals", () => {
  assert.equal(toString(frameToSeconds(df, 30n)), "1001/1000"); // 30 frames at 30000/1001 = 1.001 s
  assert.equal(toString(frameToSeconds(ndf24, 24n)), "1001/1000");
});

test("drop-frame never drops real frames: numbering is continuous", () => {
  // Adjacent frames always differ by exactly 1 in continuous numbering,
  // even across minute boundaries where labels jump.
  let prev = labelToFrame(df, 0n, 0n, 59n, 29n);
  const next = labelToFrame(df, 0n, 1n, 0n, 2n);
  assert.equal(next - prev, 1n);
  prev = labelToFrame(df, 0n, 9n, 59n, 29n);
  assert.equal(labelToFrame(df, 0n, 10n, 0n, 0n) - prev, 1n);
});
