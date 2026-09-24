import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createDiagnostic,
  relocate,
  type Diagnostic,
} from '../src/diagnostic.ts';
import { History } from '../src/history.ts';
import { SurrogatePairError } from '../src/utf16.ts';

function ins(h: History, offset: number, text: string) {
  return h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset, text });
}

function del(h: History, start: number, end: number) {
  return h.applyEdit({ kind: 'delete', id: h.nextEditId(), start, end });
}

function mov(h: History, start: number, end: number, to: number, stableId: string) {
  return h.applyEdit({ kind: 'move', id: h.nextEditId(), start, end, to, stableId });
}

test('insert before target shifts diagnostic exactly (LF)', () => {
  const h = new History('one\ntwo\nthree\n');
  const d = createDiagnostic(h, 4, 7, 'unused variable');
  ins(h, 0, 'zero\n');
  const r = relocate(h, d);
  assert.deepEqual({ kind: r.kind, ...(r.kind === 'definite' ? { start: r.start, end: r.end, via: r.via } : {}) },
    { kind: 'definite', via: 'identity', start: 9, end: 12 });
});

test('insert before target shifts diagnostic exactly (CRLF counts 2 units)', () => {
  const h = new History('one\r\ntwo\r\nthree\r\n');
  const d = createDiagnostic(h, 5, 8, 'unused variable');
  ins(h, 0, 'zero\r\n');
  const r = relocate(h, d);
  assert.ok(r.kind === 'definite' && r.via === 'identity');
  assert.deepEqual({ start: r.start, end: r.end }, { start: 11, end: 14 });
  assert.equal(h.text().slice(r.start, r.end), 'two');
});

test('insert exactly at target start shifts target (not swallowed)', () => {
  const h = new History('foo bar');
  const d = createDiagnostic(h, 0, 3);
  ins(h, 0, 'x');
  const r = relocate(h, d);
  assert.ok(r.kind === 'definite');
  assert.deepEqual({ start: r.start, end: r.end }, { start: 1, end: 4 });
});

test('full deletion invalidates; identical text elsewhere does not revive', () => {
  const h = new History('foo bar');
  const d = createDiagnostic(h, 0, 3, 'bad call');
  del(h, 0, 4);
  ins(h, h.text().length, ' foo');
  assert.equal(h.text(), 'bar foo');
  const r = relocate(h, d);
  assert.equal(r.kind, 'invalid');
  if (r.kind !== 'invalid') return;
  assert.equal(r.reason, 'target range deleted');
  assert.equal(r.excluded.length, 1);
  assert.deepEqual({ start: r.excluded[0]!.start, end: r.excluded[0]!.end }, { start: 4, end: 7 });
  assert.match(r.excluded[0]!.excluded!, /identity lost/);
  assert.ok(r.evidence.steps.some((s) => s.includes('no stable identity')));
});

test('move with stableId is followed precisely', () => {
  const h = new History('p1\np2\np3\n');
  const d = createDiagnostic(h, 3, 5, 'inside p2');
  mov(h, 3, 6, 6, 'paragraph-p2');
  assert.equal(h.text(), 'p1\np3\np2\n');
  const r = relocate(h, d);
  assert.ok(r.kind === 'definite' && r.via === 'identity');
  assert.deepEqual({ start: r.start, end: r.end }, { start: 6, end: 8 });
  assert.equal(h.text().slice(r.start, r.end), 'p2');
  assert.ok(r.evidence.steps.some((s) => s.includes('stableId=paragraph-p2')));
});

test('cut-paste without stableId does not follow (delete + insert)', () => {
  const h = new History('p1\np2\np3\n');
  const d = createDiagnostic(h, 3, 5, 'inside p2');
  del(h, 3, 6);
  ins(h, h.text().length, 'p2\n');
  assert.equal(h.text(), 'p1\np3\np2\n');
  const r = relocate(h, d);
  assert.equal(r.kind, 'invalid');
  if (r.kind !== 'invalid') return;
  assert.equal(r.excluded.length, 1);
  assert.deepEqual({ start: r.excluded[0]!.start, end: r.excluded[0]!.end }, { start: 6, end: 8 });
});

test('duplicate context yields ambiguous candidates, never arbitrary first pick', () => {
  const unit = 'foo-bar-';
  const h = new History(unit.repeat(10));
  const start = 5 * unit.length;
  const d = createDiagnostic(h, start, start + 3, 'on foo');
  ins(h, 0, '//');
  h.reclaim(0);
  assert.ok(!h.has(d.version));
  const r = relocate(h, d);
  assert.equal(r.kind, 'ambiguous');
  if (r.kind !== 'ambiguous') return;
  assert.ok(r.candidates.length >= 2);
  const starts = r.candidates.map((c) => c.start);
  assert.ok(starts.includes(start + 2));
  assert.ok(r.evidence.steps.some((s) => s.includes('refusing to pick')));
});

test('unique context fallback after history reclaim gives definite (context)', () => {
  const h = new History('alpha beta gamma delta');
  const d = createDiagnostic(h, 6, 10, 'on beta');
  ins(h, 0, 'v2 ');
  ins(h, 0, 'v3 ');
  h.reclaim(0);
  assert.ok(!h.has(d.version));
  const r = relocate(h, d);
  assert.ok(r.kind === 'definite' && r.via === 'context');
  assert.equal(h.text().slice(r.start, r.end), 'beta');
});

test('context fallback with no match is invalid', () => {
  const h = new History('alpha beta gamma');
  const d = createDiagnostic(h, 6, 10, 'on beta');
  del(h, 0, h.text().length);
  ins(h, 0, 'completely different text');
  h.reclaim(0);
  const r = relocate(h, d);
  assert.equal(r.kind, 'invalid');
  if (r.kind !== 'invalid') return;
  assert.match(r.reason, /no matching context/);
});

test('undo branch: diagnostic follows across undo and new branch edit', () => {
  const h = new History('hello world');
  ins(h, 0, 'say ');
  const d = createDiagnostic(h, 10, 15, 'on world');
  h.undo();
  ins(h, 5, '!');
  assert.equal(h.text(), 'hello! world');
  const r = relocate(h, d);
  assert.ok(r.kind === 'definite' && r.via === 'identity');
  assert.deepEqual({ start: r.start, end: r.end }, { start: 7, end: 12 });
  assert.ok(r.evidence.steps.some((s) => s.startsWith('undo of insert')));
});

test('abandoned branch reclaimed: falls back to context with evidence', () => {
  const h = new History('hello world');
  const v0 = h.current;
  ins(h, 0, 'say ');
  const d = createDiagnostic(h, 10, 15, 'on world');
  h.checkout(v0);
  ins(h, 0, 'hey ');
  h.reclaim(1);
  assert.ok(!h.has(d.version));
  const r = relocate(h, d);
  assert.ok(r.kind === 'definite' && r.via === 'context');
  assert.equal(h.text().slice(r.start, r.end), 'world');
  assert.ok(r.evidence.steps.some((s) => s.includes('reclaimed or on an abandoned branch')));
});

test('history reclaim with pinned diagnostic version keeps identity mapping', () => {
  const h = new History('alpha beta gamma');
  const d = createDiagnostic(h, 6, 10, 'on beta');
  ins(h, 0, '1 ');
  ins(h, 0, '2 ');
  ins(h, 0, '3 ');
  const gc = h.reclaim(1, new Set([d.version]));
  assert.ok(gc.checkpoint !== null);
  assert.ok(h.has(d.version));
  const r = relocate(h, d);
  assert.ok(r.kind === 'definite' && r.via === 'identity');
  assert.equal(h.text().slice(r.start, r.end), 'beta');
  assert.deepEqual({ start: r.start, end: r.end }, { start: 12, end: 16 });
});

test('branch merge: diagnostic from trunk survives merge of side branch', () => {
  const h = new History('aaa bbb ccc');
  ins(h, 0, 'A:');
  const d = createDiagnostic(h, 6, 9, 'on bbb');
  const mainTip = h.current;
  h.checkout(0);
  ins(h, 11, ':B');
  const branchTip = h.current;
  h.checkout(mainTip);
  h.merge(branchTip);
  assert.equal(h.text(), 'A:aaa bbb ccc:B');
  const r = relocate(h, d);
  assert.ok(r.kind === 'definite' && r.via === 'identity');
  assert.equal(h.text().slice(r.start, r.end), 'bbb');
});

test('diagnostic range may not split a surrogate pair', () => {
  const h = new History('a\u{1F600}b');
  assert.throws(() => createDiagnostic(h, 1, 2), SurrogatePairError);
  assert.throws(() => createDiagnostic(h, 2, 3), SurrogatePairError);
  const d = createDiagnostic(h, 1, 3);
  ins(h, 0, 'xy');
  const r = relocate(h, d);
  assert.ok(r.kind === 'definite');
  assert.deepEqual({ start: r.start, end: r.end }, { start: 3, end: 5 });
});

test('partial deletion shrinks range but stays definite with evidence', () => {
  const h = new History('abcdefgh');
  const d = createDiagnostic(h, 2, 8, 'on cdefgh');
  del(h, 5, 7);
  const r = relocate(h, d);
  assert.ok(r.kind === 'definite' && r.via === 'identity');
  assert.deepEqual({ start: r.start, end: r.end }, { start: 2, end: 6 });
  assert.ok(r.evidence.steps.some((s) => s.includes('partially deleted')));
});

test('consecutive multi-version edits compose into one precise mapping', () => {
  const h = new History('const answer = 42;\n');
  const d = createDiagnostic(h, 15, 17, 'magic number');
  ins(h, 0, '// file header\n');
  ins(h, h.text().indexOf('answer'), 'final ');
  del(h, h.text().indexOf(';'), h.text().indexOf(';') + 1);
  ins(h, h.text().length, 'export {};\n');
  const r = relocate(h, d);
  assert.ok(r.kind === 'definite' && r.via === 'identity');
  assert.equal(h.text().slice(r.start, r.end), '42');
  const composedNote = r.evidence.steps[0]!;
  assert.match(composedNote, /4 edit\(s\)/);
});
