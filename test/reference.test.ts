import { test } from 'node:test';
import assert from 'node:assert/strict';
import { History } from '../src/history.ts';
import { referenceMapRange } from '../src/reference.ts';
import type { Edit } from '../src/types.ts';

test('duplicate text: composed identity mapping matches the reference', () => {
  const h = new History('foo x foo y foo');
  const bindVersion = h.head;
  const diag = h.bind({ range: { start: 6, end: 9 } }); // middle foo
  const e1: Edit[] = [{ kind: 'insert', at: 0, text: '>>' }];
  h.commit(e1);
  const e2: Edit[] = [{ kind: 'delete', start: 15, end: 16 }];
  h.commit(e2);
  const e3: Edit[] = [{ kind: 'insert', at: 9, text: '!' }]; // inside middle foo
  h.commit(e3);
  const expected = referenceMapRange([...e1, ...e2, ...e3], { start: 6, end: 9 });
  assert.deepEqual(expected, { start: 8, end: 12 });
  const r = h.relocate(diag);
  if (r.status !== 'certain') assert.fail(`expected certain, got ${r.status}`);
  assert.deepEqual(r.range, expected);
  const mapped = h.mapRange({ start: 6, end: 9 }, bindVersion, h.head);
  assert.deepEqual(mapped, { status: 'mapped', range: expected });
});

test('whole-block move: composed mapping matches the reference', () => {
  const h = new History('intro body conclusion');
  const diag = h.bind({ range: { start: 6, end: 10 } }); // body
  const e1: Edit[] = [{ kind: 'move', start: 6, end: 10, to: 0, moveId: 'blk' }];
  h.commit(e1);
  const e2: Edit[] = [{ kind: 'insert', at: 0, text: '## ' }];
  h.commit(e2);
  assert.equal(h.text(), '## bodyintro  conclusion');
  const expected = referenceMapRange([...e1, ...e2], { start: 6, end: 10 });
  assert.deepEqual(expected, { start: 3, end: 7 });
  const r = h.relocate(diag);
  if (r.status !== 'certain') assert.fail(`expected certain, got ${r.status}`);
  assert.deepEqual(r.range, expected);
  assert.equal(h.text().slice(3, 7), 'body');
});

test('branch merge: composed mapping matches the reference across the merge', () => {
  const h = new History('alpha beta gamma');
  const e1: Edit[] = [{ kind: 'insert', at: 16, text: ' delta' }];
  const v1 = h.commit(e1);
  const diag = h.bind({ range: { start: 6, end: 10 } }); // beta
  h.undo();
  const e2: Edit[] = [{ kind: 'move', start: 6, end: 10, to: 0, moveId: 'mv-beta' }];
  const v2 = h.commit(e2);
  h.checkout(v1);
  const vm = h.merge(v2);
  const expected = referenceMapRange([...e1, ...e2], { start: 6, end: 10 });
  assert.deepEqual(expected, { start: 0, end: 4 });
  const r = h.relocate(diag, vm);
  if (r.status !== 'certain') assert.fail(`expected certain, got ${r.status}`);
  assert.deepEqual(r.range, expected);
  const mapped = h.mapRange({ start: 6, end: 10 }, v1, vm);
  assert.deepEqual(mapped, { status: 'mapped', range: expected });
});

test('mapRange composes backward along the version path', () => {
  const h = new History('hello brave world');
  const root = h.head;
  h.commit([{ kind: 'insert', at: 0, text: 'say ' }]);
  const head = h.head;
  // 'brave' sits at [10,15) in 'say hello brave world'
  const back = h.mapRange({ start: 10, end: 15 }, head, root);
  assert.deepEqual(back, { status: 'mapped', range: { start: 6, end: 11 } });
  const fwd = h.mapRange({ start: 6, end: 11 }, root, head);
  assert.deepEqual(fwd, { status: 'mapped', range: { start: 10, end: 15 } });
});

test('mapRange reports deletion and unknown endpoints honestly', () => {
  const h = new History('aaa bbb');
  const root = h.head;
  const v1 = h.commit([{ kind: 'delete', start: 4, end: 7 }]);
  assert.deepEqual(h.mapRange({ start: 4, end: 7 }, root, v1), { status: 'deleted' });
  h.undo();
  h.commit([{ kind: 'insert', at: 0, text: 'x' }]);
  h.reclaim();
  const unknown = h.mapRange({ start: 0, end: 1 }, root, v1);
  assert.equal(unknown.status, 'unknown');
});
