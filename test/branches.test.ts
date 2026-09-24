import { test } from 'node:test';
import assert from 'node:assert/strict';
import { History } from '../src/history.ts';

test('undo branches: tombstone on one branch, live marker on the other', () => {
  const h = new History('hello world');
  const diag = h.bind({ range: { start: 6, end: 11 } }); // world
  const v1 = h.commit([{ kind: 'delete', start: 6, end: 11 }]); // 'hello '
  const r1 = h.relocate(diag, v1);
  assert.equal(r1.status, 'invalid');
  h.undo();
  const v2 = h.commit([{ kind: 'insert', at: 6, text: 'brave ' }]); // 'hello brave world'
  const r2 = h.relocate(diag, v2);
  if (r2.status !== 'certain') assert.fail(`expected certain, got ${r2.status}`);
  assert.deepEqual(r2.range, { start: 12, end: 17 });
  // The tombstone branch still says invalid.
  assert.equal(h.relocate(diag, v1).status, 'invalid');
});

test('history reclamation collects abandoned branches', () => {
  const h = new History('hello world');
  const diag = h.bind({ range: { start: 6, end: 11 } });
  const v1 = h.commit([{ kind: 'delete', start: 6, end: 11 }]);
  h.undo();
  h.commit([{ kind: 'insert', at: 0, text: '>' }]);
  const removed = h.reclaim();
  assert.ok(removed.includes(v1));
  const r = h.relocate(diag, v1);
  if (r.status !== 'invalid') assert.fail(`expected invalid, got ${r.status}`);
  assert.match(r.reason, /reclaimed/);
});

test('reclaim of the bind version degrades to text-similarity with evidence', () => {
  const h = new History('cat dog cat');
  h.commit([{ kind: 'insert', at: 0, text: '1' }]);
  const v1 = h.head;
  const diag = h.bind({ range: { start: 5, end: 8 } }); // dog in '1cat dog cat'
  h.undo();
  h.commit([{ kind: 'insert', at: 0, text: '2' }]); // sibling branch
  const removed = h.reclaim();
  assert.ok(removed.includes(v1));
  const r = h.relocate(diag);
  if (r.status !== 'certain') assert.fail(`expected certain, got ${r.status}`);
  assert.equal(r.basis, 'text-similarity');
  assert.deepEqual(r.range, { start: 5, end: 8 });
  assert.match((r.evidence as string[])[0], /reclaimed/);
});

test('reclaim + duplicate text: ambiguous, never disguised as certain', () => {
  const h = new History('a cat b a cat b');
  h.commit([{ kind: 'insert', at: 0, text: '1' }]);
  const diag = h.bind({ range: { start: 3, end: 6 }, contextRadius: 2 }); // first cat
  h.undo();
  h.commit([{ kind: 'insert', at: 0, text: '2' }]);
  h.reclaim();
  const r = h.relocate(diag);
  if (r.status !== 'ambiguous') assert.fail(`expected ambiguous, got ${r.status}`);
  assert.equal(r.candidates.length, 2);
  assert.deepEqual(r.candidates[0].range, { start: 3, end: 6 });
  assert.deepEqual(r.candidates[1].range, { start: 11, end: 14 });
});

test('merge follows a stable-id move replayed from the merged branch', () => {
  const h = new History('alpha beta gamma');
  const v1 = h.commit([{ kind: 'insert', at: 16, text: ' delta' }]); // main
  const diag = h.bind({ range: { start: 6, end: 10 } }); // beta
  h.undo();
  const v2 = h.commit([{ kind: 'move', start: 6, end: 10, to: 0, moveId: 'mv-beta' }]);
  h.checkout(v1);
  const vm = h.merge(v2);
  assert.equal(h.text(vm), 'betaalpha  gamma delta');
  const r = h.relocate(diag, vm);
  if (r.status !== 'certain') assert.fail(`expected certain, got ${r.status}`);
  assert.equal(r.basis, 'identity');
  assert.deepEqual(r.range, { start: 0, end: 4 });
  const trail = r.evidence as { kind: string; detail: string }[];
  assert.ok(trail.some((e) => e.kind === 'move-follow' && e.detail.includes('mv-beta')));
});
