import test from 'node:test';
import assert from 'node:assert/strict';
import { TruncationEngine } from '../src/engine.ts';
import { metricsV1 } from '../fixtures/metrics.ts';
import { fileName, keyChoice, keyLog } from '../fixtures/trees.ts';
import { lineText } from './helpers.ts';

test('middle ellipsis keeps head and tail of a single line', () => {
  const engine = new TruncationEngine(fileName, metricsV1, { width: 60, maxLines: 1, mode: 'middle' });
  const r = engine.lastLayout;
  assert.equal(r.truncated, true);
  assert.equal(r.lines.length, 1);
  // 8 clusters fit the 50px budget next to the 10px ellipsis, balanced 4+4
  assert.equal(lineText(r, 0), 'annu….pdf');
});

test('key mode prefers the fragment with more key clusters', () => {
  const wide = new TruncationEngine(keyChoice, metricsV1, { width: 62, maxLines: 1, mode: 'key' });
  // 3+5 key clusters beat keeping STARTER (7) alone
  assert.equal(lineText(wide.lastLayout, 0), 'STA…ENDER');
  const narrow = new TruncationEngine(keyChoice, metricsV1, { width: 46, maxLines: 1, mode: 'key' });
  assert.equal(lineText(narrow.lastLayout, 0), 'S…ENDER');
});

test('required key fragment survives, plain text is elided', () => {
  const engine = new TruncationEngine(keyLog, metricsV1, { width: 100, maxLines: 1, mode: 'key' });
  const r = engine.lastLayout;
  assert.equal(lineText(r, 0), 'E…E_CONN_REFUSED');
  assert.equal(r.conflicts.length, 0);
});
