import { test } from 'node:test';
import assert from 'node:assert/strict';
import { History } from '../src/history.ts';
import { referenceMapRange } from '../src/reference.ts';
import type { Edit } from '../src/types.ts';

test('insert before the target shifts it precisely', () => {
  const h = new History('hello world foo');
  const diag = h.bind({ range: { start: 6, end: 11 }, message: 'world' });
  h.commit([{ kind: 'insert', at: 0, text: 'say ' }]);
  const r = h.relocate(diag);
  if (r.status !== 'certain') assert.fail(`expected certain, got ${r.status}`);
  assert.equal(r.basis, 'identity');
  assert.deepEqual(r.range, { start: 10, end: 15 });
  assert.equal(h.text().slice(10, 15), 'world');
  const kinds = (r.evidence as { kind: string }[]).map((e) => e.kind);
  assert.ok(kinds.includes('shift'));
});

test('multi-version edits compose and match the step-by-step reference', () => {
  const h = new History('alpha beta gamma delta');
  const diag = h.bind({ range: { start: 6, end: 10 } }); // beta
  const e1: Edit[] = [{ kind: 'insert', at: 0, text: '>> ' }];
  h.commit(e1);
  const e2: Edit[] = [{ kind: 'delete', start: 0, end: 2 }];
  h.commit(e2);
  const e3: Edit[] = [{ kind: 'insert', at: 9, text: 'X' }]; // lands inside beta
  h.commit(e3);
  assert.equal(h.text(), ' alpha beXta gamma delta');
  const r = h.relocate(diag);
  if (r.status !== 'certain') assert.fail(`expected certain, got ${r.status}`);
  const expected = referenceMapRange([...e1, ...e2, ...e3], { start: 6, end: 10 });
  assert.deepEqual(r.range, expected);
  assert.deepEqual(r.range, { start: 7, end: 12 });
});

test('full deletion invalidates; identical text elsewhere does not revive', () => {
  const h = new History('aaa bbb ccc');
  const diag = h.bind({ range: { start: 4, end: 7 } }); // bbb
  h.commit([{ kind: 'delete', start: 4, end: 7 }]);
  let r = h.relocate(diag);
  if (r.status !== 'invalid') assert.fail(`expected invalid, got ${r.status}`);
  // The exact same text appears elsewhere: still invalid (tombstone).
  h.commit([{ kind: 'insert', at: 0, text: 'bbb ' }]);
  r = h.relocate(diag);
  if (r.status !== 'invalid') assert.fail(`expected invalid, got ${r.status}`);
  assert.match(r.reason, /deleted/);
  const kinds = (r.evidence as { kind: string }[]).map((e) => e.kind);
  assert.ok(kinds.includes('deleted'));
});

test('move with a stable id is followed precisely', () => {
  const h = new History('one two three');
  const diag = h.bind({ range: { start: 4, end: 7 } }); // two
  h.commit([{ kind: 'move', start: 4, end: 7, to: 0, moveId: 'mv-1' }]);
  assert.equal(h.text(), 'twoone  three');
  const r = h.relocate(diag);
  if (r.status !== 'certain') assert.fail(`expected certain, got ${r.status}`);
  assert.deepEqual(r.range, { start: 0, end: 3 });
  const trail = r.evidence as { kind: string; detail: string }[];
  assert.ok(trail.some((e) => e.kind === 'move-follow' && e.detail.includes('mv-1')));
});

test('move without a stable id degrades to delete+insert: no follow, no revive', () => {
  const h = new History('one two three');
  const diag = h.bind({ range: { start: 4, end: 7 } }); // two
  h.commit([{ kind: 'move', start: 4, end: 7, to: 0 }]);
  assert.equal(h.text(), 'twoone  three'); // 'two' exists at [0,3)
  const r = h.relocate(diag);
  if (r.status !== 'invalid') assert.fail(`expected invalid, got ${r.status}`);
  assert.match(r.reason, /deleted/);
});

test('partial deletion shrinks the range', () => {
  const h = new History('aa bbXbb cc');
  const diag = h.bind({ range: { start: 3, end: 8 } }); // bbXbb
  h.commit([{ kind: 'delete', start: 4, end: 6 }]); // remove 'bX'
  assert.equal(h.text(), 'aa bbb cc');
  const r = h.relocate(diag);
  if (r.status !== 'certain') assert.fail(`expected certain, got ${r.status}`);
  assert.deepEqual(r.range, { start: 3, end: 6 });
});

test('CRLF document: identity tracking stays in UTF-16 units', () => {
  const h = new History('line1\r\nline2\r\n');
  const diag = h.bind({ range: { start: 7, end: 12 } }); // line2
  h.commit([{ kind: 'insert', at: 0, text: 'x' }]);
  const r = h.relocate(diag);
  if (r.status !== 'certain') assert.fail(`expected certain, got ${r.status}`);
  assert.deepEqual(r.range, { start: 8, end: 13 });
  assert.equal(h.text().slice(8, 13), 'line2');
});
