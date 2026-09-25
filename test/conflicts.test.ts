import test from 'node:test';
import assert from 'node:assert/strict';
import { TruncationEngine } from '../src/engine.ts';
import { metricsV1 } from '../fixtures/metrics.ts';
import { conflictingKeys } from '../fixtures/trees.ts';
import { allText } from './helpers.ts';

test('required fragments exceeding capacity together raise an explicit conflict', () => {
  // two required 60px entities + 10px ellipsis can never fit 100px
  const engine = new TruncationEngine(conflictingKeys, metricsV1, { width: 100, maxLines: 1, mode: 'key' });
  const r = engine.lastLayout;
  assert.equal(r.conflicts.length, 1);
  const c = r.conflicts[0];
  assert.equal(c.type, 'key-overflow');
  assert.equal(c.requiredWidth, 120);
  assert.equal(c.capacity, 90);
  assert.deepEqual(c.keyIds, ['k1']); // best effort keeps one required entity
  assert.ok(c.message.includes('k1'));
  // best-effort output still keeps the other required entity whole
  assert.ok(allText(r).includes('BBBBBBBBBB'));
  assert.ok(!allText(r).includes('AAAAAAAAAA'));
});

test('both required fragments fit when capacity allows: no conflict', () => {
  const engine = new TruncationEngine(conflictingKeys, metricsV1, { width: 140, maxLines: 1, mode: 'key' });
  const r = engine.lastLayout;
  assert.equal(r.conflicts.length, 0);
  assert.ok(allText(r).includes('AAAAAAAAAA'));
  assert.ok(allText(r).includes('BBBBBBBBBB'));
});
