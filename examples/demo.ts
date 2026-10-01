import {
  Timecode,
  Timeline,
  ntscDropFrame,
  compileRankIndex,
  frameToSamples,
  samplesToFrame,
  ratToString,
  ratToDecimal,
  FPS_29_97,
  FPS_24,
} from '../src/index.ts';

const show = (title: string, lines: [string, unknown][]) => {
  console.log(`\n== ${title} ==`);
  for (const [k, v] of lines) console.log(`  ${k.padEnd(44)} ${v}`);
};

const dfRule = ntscDropFrame(30);
const df = new Timecode({ rate: FPS_29_97, rule: dfRule });

show('drop-frame period table (NTSC 29.97, nominal 30)', [
  ['rule id', dfRule.id],
  ['period (nominal frames)', dfRule.periodFrames],
  ['dropped labels per period', dfRule.drops.length],
  ['valid labels per period', dfRule.validPerPeriod],
  ['first drops', dfRule.drops.slice(0, 4).join(', ')],
]);

show('frame <-> label (minute boundary skips labels, not frames)', [
  ['frame 1799', df.frameToLabel(1799n)],
  ['frame 1800 (no frames lost)', df.frameToLabel(1800n)],
  ['frame 17982 (10-min boundary)', df.frameToLabel(17982n)],
  ['frame 107892 (1 hour)', df.frameToLabel(107892n)],
  ['label 00:01:00;02 -> frame', df.frameOf('00:01:00;02')],
]);

const bad = df.parse('00:01:00;00');
show('nonexistent label -> nearest legal candidate', [
  ['parse 00:01:00;00 ok?', bad.ok],
  ['kind', bad.ok ? '-' : bad.kind],
  ['nearest label', bad.ok ? '-' : bad.kind === 'dropped-label' ? bad.nearest.label : '-'],
  ['nearest frame', bad.ok ? '-' : bad.kind === 'dropped-label' ? bad.nearest.frame : '-'],
]);

const one = frameToSamples(1n, FPS_29_97, 48000n, 'nearest-half-away');
const back = samplesToFrame(one.samples, FPS_29_97, 48000n, 'nearest-half-away');
show('audio sample alignment at 48 kHz (exact rational, explicit rounding)', [
  ['1 frame exact samples', `${ratToString(one.exact)} = ${ratToDecimal(one.exact, 4)}`],
  ['rounded samples', one.samples],
  ['rounding error (rational)', ratToString(one.error)],
  ['samples -> frame round trip', back.frame],
]);

const tl = new Timeline([
  { name: 'opening', rate: FPS_29_97, rule: dfRule, frames: 1800 },
  { name: 'body', rate: FPS_24, rule: null, frames: 1000 },
]);
const bounds = tl.boundarySamples(48000n);
const at = tl.locateBySamples(2884879n, 48000n, 'nearest-half-even');
show('cross-rule splice: 29.97 DF segment + 24 NDF segment', [
  ['total frames', tl.totalFrames],
  ['boundary samples (exact)', bounds.map(ratToString).join(' -> ')],
  ['label at global frame 1800', tl.labelAt(1800n)],
  ['sample 2884879 -> segment', at.segment],
  ['sample 2884879 -> local frame / label', `${at.localFrame} / ${at.label}`],
  ['alignment error (rational samples)', ratToString(at.sampleError)],
]);

const idx = compileRankIndex(dfRule);
const big = 10n ** 15n;
const signed = new Timecode({ rate: FPS_29_97, rule: dfRule, negative: 'signed' });
show('period jumps: huge and negative positions, no scanning', [
  ['rank 10^15 -> nominal', idx.rankToNominal(big)],
  ['round trip ok?', idx.nominalToRank(idx.rankToNominal(big)) === big],
  ['frame 10^15 label', df.frameToLabel(big)],
  ['frame -1800 (signed policy)', signed.frameToLabel(-1800n)],
  ['label -00:01:00;02 -> frame', signed.frameOf('-00:01:00;02')],
]);
