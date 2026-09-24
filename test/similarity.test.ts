import { test } from 'node:test';
import assert from 'node:assert/strict';
import { History } from '../src/history.ts';

test('fallback after undo: unique match is certain via text-similarity', () => {
  const h = new History('alpha beta gamma');
  h.commit([{ kind: 'insert', at: 0, text: 'x ' }]);
  const diag = h.bind({ range: { start: 8, end: 12 } }); // beta in 'x alpha beta gamma'
  h.undo(); // marker only exists on the undone branch
  const r = h.relocate(diag);
  if (r.status !== 'certain') assert.fail(`expected certain, got ${r.status}`);
  assert.equal(r.basis, 'text-similarity');
  assert.deepEqual(r.range, { start: 6, end: 10 });
});

test('duplicates with distinct context: best match wins, loser excluded with reason', () => {
  const h = new History('foo bar foo');
  h.commit([{ kind: 'insert', at: 0, text: 'z' }]);
  const diag = h.bind({ range: { start: 1, end: 4 } }); // first foo in 'zfoo bar foo'
  h.undo();
  const r = h.relocate(diag);
  if (r.status !== 'certain') assert.fail(`expected certain, got ${r.status}`);
  assert.equal(r.basis, 'text-similarity');
  assert.deepEqual(r.range, { start: 0, end: 3 });
  assert.equal(r.excluded.length, 1);
  assert.deepEqual(r.excluded[0].range, { start: 8, end: 11 });
  assert.match(r.excluded[0].reason, /context score/);
});

test('identical context twice: ambiguous with both candidates, never a first-pick', () => {
  const h = new History('x foo y x foo y');
  h.commit([{ kind: 'insert', at: 0, text: 'Q' }]);
  const diag = h.bind({ range: { start: 3, end: 6 }, contextRadius: 2 }); // first foo
  h.undo();
  const r = h.relocate(diag);
  if (r.status !== 'ambiguous') assert.fail(`expected ambiguous, got ${r.status}`);
  assert.equal(r.candidates.length, 2);
  assert.deepEqual(r.candidates[0].range, { start: 2, end: 5 });
  assert.deepEqual(r.candidates[1].range, { start: 10, end: 13 });
  assert.ok(r.candidates.every((c) => c.evidence.includes('occurrence')));
  assert.ok(r.evidence.some((e) => e.includes('tie')));
});

test('target text gone: invalid with search evidence', () => {
  const h = new History('nothing here');
  h.commit([{ kind: 'insert', at: 0, text: 'zzz ' }]);
  const diag = h.bind({ range: { start: 0, end: 3 } }); // zzz
  h.undo();
  const r = h.relocate(diag);
  if (r.status !== 'invalid') assert.fail(`expected invalid, got ${r.status}`);
  assert.match(r.reason, /not found/);
});

test('CRLF vs LF: line-ending-insensitive fallback reports UTF-16 offsets', () => {
  const h = new History('line1\r\nline2\r\n');
  h.commit([{ kind: 'insert', at: 0, text: '!' }]);
  const diag = h.bind({ range: { start: 8, end: 13 } }); // line2 in '!line1\r\nline2\r\n'
  h.undo();
  const r = h.relocate(diag);
  if (r.status !== 'certain') assert.fail(`expected certain, got ${r.status}`);
  assert.equal(r.basis, 'text-similarity');
  assert.deepEqual(r.range, { start: 7, end: 12 });
  assert.equal(h.text().slice(7, 12), 'line2');
});
