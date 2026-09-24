import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LanguageService } from '../src/index.ts';

test('depth 10000: nesting matches iteratively, no stack overflow', () => {
  const depth = 10_000;
  const text = '('.repeat(depth) + 'x' + ')'.repeat(depth);
  const svc = new LanguageService(text);

  assert.equal(svc.getDiagnostics().length, 0);
  assert.equal(svc.getPairs().length, depth);
  assert.equal(svc.matchAt(0), text.length - 1);
  assert.equal(svc.matchAt(text.length - 1), 0);

  const inner = svc.enclosingPair(depth)!; // offset of 'x'
  assert.deepEqual([inner.open, inner.close], [depth - 1, depth + 1]);
  assert.equal(inner.depth, depth);
});

test('depth 10000 with mixed bracket types', () => {
  const cycle = ['(', '[', '{'];
  const closeFor: Record<string, string> = { '(': ')', '[': ']', '{': '}' };
  const depth = 10_000;
  let opens = '';
  let closes = '';
  for (let i = 0; i < depth; i++) {
    const ch = cycle[i % 3];
    opens += ch;
    closes = closeFor[ch] + closes;
  }
  const svc = new LanguageService(opens + closes);
  assert.equal(svc.getDiagnostics().length, 0);
  assert.equal(svc.getPairs().length, depth);
  assert.equal(svc.matchAt(0), opens.length + closes.length - 1);
});

test('depth 10000: one missing closer yields exactly one unclosed opener', () => {
  const depth = 10_000;
  const svc = new LanguageService('('.repeat(depth) + ')'.repeat(depth - 1));
  const diags = svc.getDiagnostics();
  assert.equal(diags.length, 1);
  assert.equal(diags[0].code, 'unclosed-open');
  assert.equal(diags[0].start, 0); // the outermost "(" stays open
  assert.equal(svc.getPairs().length, depth - 1);
});

test('depth 10000: local edit near the top re-analyzes correctly', () => {
  const depth = 10_000;
  const svc = new LanguageService('('.repeat(depth) + ')'.repeat(depth));
  // delete the very first "(" -> one unmatched ")" at the end
  svc.applyEdit(0, 1, '');
  const diags = svc.getDiagnostics();
  assert.equal(diags.length, 1);
  assert.equal(diags[0].code, 'unmatched-close');
  assert.equal(diags[0].start, 2 * depth - 2);
  assert.equal(svc.getPairs().length, depth - 1);
});

test('depth 100000 for good measure', () => {
  const depth = 100_000;
  const svc = new LanguageService('['.repeat(depth) + ']'.repeat(depth));
  assert.equal(svc.getDiagnostics().length, 0);
  assert.equal(svc.getPairs().length, depth);
  const mid = svc.enclosingPair(depth - 1)!;
  assert.equal(mid.depth, depth - 1);
});
