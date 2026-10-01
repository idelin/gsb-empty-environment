import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Timecode } from '../src/timecode.ts';
import { ntscDropFrame } from '../src/dropframe.ts';
import { FPS_29_97, FPS_24, FPS_25 } from '../src/rate.ts';

const df = new Timecode({ rate: FPS_29_97, rule: ntscDropFrame(30) });
const ndf = new Timecode({ rate: FPS_24 });

test('drop-frame landmarks: frame -> label', () => {
  assert.equal(df.frameToLabel(0n), '00:00:00;00');
  assert.equal(df.frameToLabel(1799n), '00:00:59;29');
  assert.equal(df.frameToLabel(1800n), '00:01:00;02');
  assert.equal(df.frameToLabel(3598n), '00:02:00;02');
  assert.equal(df.frameToLabel(17981n), '00:09:59;29');
  assert.equal(df.frameToLabel(17982n), '00:10:00;00');
  assert.equal(df.frameToLabel(107892n), '01:00:00;00');
  assert.equal(df.frameToLabel(2589408n), '24:00:00;00');
});

test('drop-frame landmarks: label -> frame', () => {
  assert.equal(df.frameOf('00:00:00;00'), 0n);
  assert.equal(df.frameOf('00:00:59;29'), 1799n);
  assert.equal(df.frameOf('00:01:00;02'), 1800n);
  assert.equal(df.frameOf('00:10:00;00'), 17982n);
  assert.equal(df.frameOf('01:00:00;00'), 107892n);
  assert.equal(df.frameOf('24:00:00;00'), 2589408n);
});

test('non-drop labels use colon and never skip', () => {
  assert.equal(ndf.frameToLabel(0n), '00:00:00:00');
  assert.equal(ndf.frameToLabel(24n * 60n), '00:01:00:00');
  assert.equal(ndf.frameOf('00:01:00:00'), 1440n);
  assert.equal(ndf.frameOf('00:01:00:01'), 1441n);
});

test('minute-boundary skip follows the rule, not a hardcoded pattern', () => {
  const r1 = df.parse('00:01:00;00');
  assert.equal(r1.ok, false);
  if (!r1.ok && r1.kind === 'dropped-label') {
    assert.equal(r1.nearest.label, '00:00:59;29');
    assert.equal(r1.nearest.frame, 1799n);
    assert.equal(r1.nearest.distance, -1n);
  } else assert.fail('expected dropped-label');
  const r2 = df.parse('00:01:00;01');
  assert.equal(r2.ok, false);
  if (!r2.ok && r2.kind === 'dropped-label') {
    assert.equal(r2.nearest.label, '00:01:00;02');
    assert.equal(r2.nearest.frame, 1800n);
    assert.equal(r2.nearest.distance, 1n);
  } else assert.fail('expected dropped-label');
  assert.equal(df.isValidLabel('00:10:00;00'), true);
  assert.equal(df.isValidLabel('00:20:00;01'), true);
  assert.throws(() => df.frameOf('00:01:00;00'), /nearest legal label is 00:00:59;29/);
});

test('malformed labels are rejected with reasons', () => {
  const bad = ['0:00:00;00', '00:60:00;00', '00:00:60;00', '00:00:00;30', '00:00:00', '00:00:00;0', 'ab:cd:ef;gh'];
  for (const b of bad) {
    const r = df.parse(b);
    assert.equal(r.ok, false, b);
    if (!r.ok) assert.equal(r.kind, 'malformed', b);
  }
  assert.equal(ndf.parse('00:00:00;00').ok, false);
  assert.equal(df.frameOf('00:01:00:02'), 1800n);
});

test('label length policy: HH >= 2 digits, unbounded; MM/SS/FF exactly 2', () => {
  const tc25 = new Timecode({ rate: FPS_25 });
  assert.equal(tc25.frameToLabel(25n * 3600n * 1000n), '1000:00:00:00');
  assert.equal(tc25.frameOf('1000:00:00:00'), 25n * 3600n * 1000n);
  assert.equal(tc25.parse('1:00:00:00').ok, false);
  assert.equal(tc25.parse('000:00:00:00').ok, true);
  assert.equal(tc25.frameOf('000:00:00:00'), 0n);
});

test('negative policy: reject by default, signed on request', () => {
  assert.throws(() => df.frameToLabel(-1n), /negative policy/);
  assert.equal(df.parse('-00:00:00;01').ok, false);
  const signed = new Timecode({ rate: FPS_29_97, rule: ntscDropFrame(30), negative: 'signed' });
  assert.equal(signed.frameToLabel(-1n), '-00:00:00;01');
  assert.equal(signed.frameToLabel(-1800n), '-00:01:00;02');
  assert.equal(signed.frameOf('-00:00:00;01'), -1n);
  assert.equal(signed.frameOf('-00:01:00;02'), -1800n);
  assert.equal(signed.parse('-00:00:00;00').ok, false);
  for (let f = -4000n; f <= 4000n; f++) {
    assert.equal(signed.frameOf(signed.frameToLabel(f)), f, `frame ${f}`);
  }
});

test('exhaustive round-trip: first 20000 frames (full period + minute boundaries)', () => {
  for (let f = 0n; f <= 20000n; f++) {
    const label = df.frameToLabel(f);
    assert.equal(df.frameOf(label), f, `frame ${f} label ${label}`);
  }
});

test('period boundary neighborhoods round-trip exhaustively (periods 1..20)', () => {
  for (let k = 1n; k <= 20n; k++) {
    for (let d = -64n; d <= 64n; d++) {
      const f = k * 17982n + d;
      assert.equal(df.frameOf(df.frameToLabel(f)), f, `frame ${f}`);
    }
  }
});

test('huge frames: period-jump conversion stays exact and instant', () => {
  for (const f of [10n ** 9n, 10n ** 12n, 10n ** 15n, 10n ** 18n]) {
    const label = df.frameToLabel(f);
    assert.equal(df.frameOf(label), f, `frame ${f} label ${label}`);
  }
});

test('rule/rate mismatch is rejected', () => {
  assert.throws(() => new Timecode({ rate: FPS_24, rule: ntscDropFrame(30) }), /nominal/);
});
