import {
  compileRule,
  frameToTimecode,
  labelToFrame,
  frameToLabel,
  parseLabel,
  parseLabelOrNearest,
  frameToSample,
  frameToSampleExact,
  sampleToFrame,
  frameToSeconds,
} from "../src/timecode.ts";
import { Timeline } from "../src/timeline.ts";
import { rat, toString, toDecimalString } from "../src/rational.ts";

const show = (title: string, lines: Array<[string, unknown]>) => {
  console.log(`\n== ${title} ==`);
  for (const [k, v] of lines) console.log(`  ${k.padEnd(46)} ${v}`);
};

// Drop-frame rule described by an explicit period table:
// every 60 s period, labels ;00 and ;01 do not exist, except every 10th period.
const df2997 = compileRule(
  "ntsc-29.97-df",
  { nominalFps: 30, periodSeconds: 60, dropFrameLabels: [0, 1], keepEveryPeriods: 10 },
  rat(30000n, 1001n),
);
const ndf23976 = compileRule(
  "film-23.976-ndf",
  { nominalFps: 24, periodSeconds: 60, dropFrameLabels: [] },
  rat(24000n, 1001n),
);

show("drop-frame 是标签规则，不是丢帧（编号连续）", [
  ["frame 1799 -> label", frameToTimecode(df2997, 1799n)],
  ["frame 1800 -> label (分钟边界跳号)", frameToTimecode(df2997, 1800n)],
  ["frame 17982 -> label (10 分钟豁免)", frameToTimecode(df2997, 17982n)],
  ["00:00:59;29 与 00:01:00;02 帧号差", String(labelToFrame(df2997, 0n, 1n, 0n, 2n) - labelToFrame(df2997, 0n, 0n, 59n, 29n))],
  ["1 小时帧数（标准值 107892）", labelToFrame(df2997, 1n, 0n, 0n, 0n)],
]);

show("非法标签：严格拒绝 + 最近合法候选", [
  ["parse 00:01:00;00", (() => { try { return parseLabel(df2997, "00:01:00;00"); } catch (e) { return `拒绝 (${e.reason}): ${e.message.slice(0, 60)}...`; } })()],
  ["parseLabelOrNearest 00:01:00;00", JSON.stringify(parseLabelOrNearest(df2997, "00:01:00;00"), (_, v) => typeof v === "bigint" ? `${v}n` : v)],
  ["parse 00:01:00:02 (错误分隔符)", (() => { try { return parseLabel(df2997, "00:01:00:02"); } catch (e) { return `拒绝 (${e.reason})`; } })()],
]);

show("长时段：周期跳跃（10^15 帧 ≈ 1056 年，O(1) 往返）", (() => {
  const big = 10n ** 15n;
  const label = frameToLabel(df2997, big);
  const back = labelToFrame(df2997, label.h, label.m, label.s, label.f);
  return [
    ["frame 10^15 -> label", frameToTimecode(df2997, big)],
    ["label -> frame 往返一致", back === big],
  ];
})());

show("帧号 <-> 样本号（29.97 @ 48 kHz，显式舍入）", [
  ["frame 1 精确样本数（有理数）", toString(frameToSampleExact(df2997, 1n, 48000n)) + " = 1601.6"],
  ["frame 1 -> sample (half-up)", frameToSample(df2997, 1n, 48000n, "half-up")],
  ["frame 1 -> sample (floor)", frameToSample(df2997, 1n, 48000n, "floor")],
  ["sample 1602 -> frame (half-up)", sampleToFrame(df2997, 1602n, 48000n, "half-up")],
  ["frame 30 起始时刻（秒，有理数）", toString(frameToSeconds(df2997, 30n)) + " s"],
]);

show("跨规则版本拼接 + 音频样本对齐（误差为有理数）", (() => {
  const tl = new Timeline([
    { rule: df2997, sourceStartFrame: 0n, frames: 1800n },
    { rule: ndf23976, sourceStartFrame: 100n, frames: 240n },
  ]);
  const cut1 = tl.audioCut(1n, 48000n, "half-up");
  const cut1800 = tl.audioCut(1800n, 48000n, "half-up");
  const cut1900 = tl.audioCut(1900n, 48000n, "half-up");
  return [
    ["总帧数（1800 DF + 240 NDF24）", tl.totalFrames],
    ["切点 1 精确时刻", toString(cut1.time) + " s"],
    ["切点 1 对齐样本 / 误差", `${cut1.sample} / ${toString(cut1.error)} s (${toDecimalString(cut1.error, 9)} s)`],
    ["切点 1800（跨规则接缝）时刻", toString(cut1800.time) + " s = " + toDecimalString(cut1800.time, 6) + " s"],
    ["切点 1800 对齐样本 / 误差", `${cut1800.sample} / ${toString(cut1800.error)} s`],
    ["切点 1900 时刻（接缝后 100 帧 23.976）", toString(cut1900.time) + " s"],
    ["切点 1900 对齐样本 / 误差", `${cut1900.sample} / ${toString(cut1900.error)} s`],
  ];
})());
