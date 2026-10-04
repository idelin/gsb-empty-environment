import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Dec } from '../src/decimal.ts';
import * as Iv from '../src/interval.ts';
import { Domain } from '../src/multi.ts';

const d = (s: string) => Dec.fromString(s);

test('zero-crossing division splits into multiple domain parts', () => {
  const num = Domain.of(Iv.point(d('1')));
  const den = Domain.of(Iv.range(d('-2'), d('3')));
  const r = num.div(den, 10, { mode: 'split', maxParts: 8 });
  assert.equal(r.precisionLoss, false);
  assert.equal(r.parts.length, 2);
  assert.equal(Iv.toString(r.parts[0]), '[-∞, -0.5]');
  assert.equal(Iv.toString(r.parts[1]), '[0.3333333333, ∞]');
  assert.equal(r.parts.some((p) => Iv.containsValue(p, d('0'))), false);
  assert.equal(r.parts.some((p) => Iv.containsValue(p, d('-1'))), true);
  assert.equal(r.parts.some((p) => Iv.containsValue(p, d('1'))), true);
});

test('reject mode throws on zero crossing', () => {
  const num = Domain.of(Iv.point(d('1')));
  const den = Domain.of(Iv.range(d('-2'), d('3')));
  assert.throws(() => num.div(den, 10, { mode: 'reject', maxParts: 8 }), Iv.ZeroDivisionError);
});

test('part-count cap forces a conservative hull with a precision-loss note', () => {
  const num = Domain.of(Iv.point(d('1')));
  const den = Domain.of(Iv.range(d('-2'), d('3')));
  const r = num.div(den, 10, { mode: 'split', maxParts: 1 });
  assert.equal(r.precisionLoss, true);
  assert.match(r.note ?? '', /precision lost/);
  assert.equal(r.parts[0].kind, 'unknown');
});

test('intersect and union', () => {
  const a = Domain.of(Iv.range(d('0'), d('2')));
  const b = Domain.of(Iv.range(d('1'), d('3')));
  const i = a.intersect(b);
  assert.equal(i.parts.length, 1);
  assert.equal(Iv.toString(i.parts[0]), '[1, 2]');
  const u = a.union(b, 1);
  assert.equal(u.precisionLoss, true);
  assert.equal(Iv.toString(u.parts[0]), '[0, 3]');
  const u2 = a.union(b, 4);
  assert.equal(u2.precisionLoss, false);
  assert.equal(u2.parts.length, 2);
});
