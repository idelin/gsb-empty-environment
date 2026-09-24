import { test } from 'node:test';
import assert from 'node:assert/strict';
import { merge3, computeEdits } from './merge.ts';

test('non-overlapping edits (first line vs last line) auto-merge', () => {
  const base = 'alpha\nbeta\ngamma\n';
  const ours = 'ALPHA\nbeta\ngamma\n'; // changed first line
  const theirs = 'alpha\nbeta\nGAMMA\n'; // changed last line
  const r = merge3(base, ours, theirs);
  assert.equal(r.clean, true);
  assert.equal(r.conflicts.length, 0);
  assert.equal(r.text, 'ALPHA\nbeta\nGAMMA\n');
});

test('same-position insertions with different content conflict (declared rule)', () => {
  const base = 'a\nb\n';
  const ours = 'a\nX\nb\n'; // insert X before line 1
  const theirs = 'a\nY\nb\n'; // insert Y before line 1
  const r = merge3(base, ours, theirs);
  assert.equal(r.clean, false);
  assert.equal(r.conflicts.length, 1);
  const c = r.conflicts[0];
  assert.deepEqual(c.base, []);
  assert.deepEqual(c.ours, ['X']);
  assert.deepEqual(c.theirs, ['Y']);
  // nothing dropped: both sides present in the merged text
  assert.ok(r.text.includes('X'));
  assert.ok(r.text.includes('Y'));
  assert.ok(r.text.includes('<<<<<<< ours'));
  assert.ok(r.text.includes('>>>>>>> theirs'));
  // untouched base content survives around the conflict
  assert.ok(r.text.startsWith('a\n'));
  assert.ok(r.text.endsWith('b\n'));
});

test('identical changes on both sides appear exactly once', () => {
  const base = 'one\ntwo\nthree\n';
  const ours = 'one\nTWO\nthree\n';
  const theirs = 'one\nTWO\nthree\n';
  const r = merge3(base, ours, theirs);
  assert.equal(r.clean, true);
  assert.equal(r.text, 'one\nTWO\nthree\n');
  assert.equal(r.text.split('TWO').length - 1, 1);
});

test('identical insertions at the same position appear exactly once', () => {
  const base = 'one\ntwo\n';
  const ours = 'one\nNEW\ntwo\n';
  const theirs = 'one\nNEW\ntwo\n';
  const r = merge3(base, ours, theirs);
  assert.equal(r.clean, true);
  assert.equal(r.text, 'one\nNEW\ntwo\n');
});

test('overlapping different edits to the same line conflict, keeping base+ours+theirs', () => {
  const base = 'start\nmiddle\nend\n';
  const ours = 'start\nours-edit\nend\n';
  const theirs = 'start\ntheirs-edit\nend\n';
  const r = merge3(base, ours, theirs);
  assert.equal(r.clean, false);
  assert.equal(r.conflicts.length, 1);
  const c = r.conflicts[0];
  assert.deepEqual(c.base, ['middle']);
  assert.deepEqual(c.ours, ['ours-edit']);
  assert.deepEqual(c.theirs, ['theirs-edit']);
  assert.ok(r.text.includes('middle'));
  assert.ok(r.text.includes('ours-edit'));
  assert.ok(r.text.includes('theirs-edit'));
});

test('CRLF base: output keeps CRLF everywhere, including inserted lines', () => {
  const base = 'alpha\r\nbeta\r\ngamma\r\n';
  const ours = 'ALPHA\r\nbeta\r\ngamma\r\n';
  const theirs = 'alpha\r\nbeta\r\nGAMMA\r\n';
  const r = merge3(base, ours, theirs);
  assert.equal(r.clean, true);
  assert.equal(r.eol, '\r\n');
  assert.equal(r.text, 'ALPHA\r\nbeta\r\nGAMMA\r\n');
  assert.ok(!/(?<!\r)\n/.test(r.text)); // no bare LF
});

test('CRLF base: conflict markers also use CRLF', () => {
  const base = 'a\r\nb\r\n';
  const ours = 'a\r\nX\r\nb\r\n';
  const theirs = 'a\r\nY\r\nb\r\n';
  const r = merge3(base, ours, theirs);
  assert.equal(r.clean, false);
  assert.ok(!/(?<!\r)\n/.test(r.text));
  assert.ok(r.text.includes('<<<<<<< ours\r\n'));
  assert.ok(r.text.includes('>>>>>>> theirs\r\n'));
});

test('LF base stays LF even when a side uses CRLF', () => {
  const base = 'a\nb\n';
  const ours = 'A\nb\n';
  const theirs = 'a\r\nB\r\n'; // theirs arrived with CRLF
  const r = merge3(base, ours, theirs);
  assert.equal(r.clean, true);
  assert.equal(r.text, 'A\nB\n');
});

test('missing trailing newline in base is preserved', () => {
  const base = 'a\nb';
  const ours = 'A\nb';
  const theirs = 'a\nB';
  const r = merge3(base, ours, theirs);
  assert.equal(r.clean, true);
  assert.equal(r.text, 'A\nB');
});

test('UTF-16 offsets: surrogate pairs are never split', () => {
  // '😀' is U+1F600 = 2 UTF-16 code units.
  const base = '😀 first\nmid 😀\nlast\n';
  const ours = '😀 first\nmid 😀 ours\nlast\n';
  const theirs = '😀 first\nmid 😀 theirs\nlast\n';
  const r = merge3(base, ours, theirs);
  assert.equal(r.clean, false);
  const c = r.conflicts[0];
  // base offsets are UTF-16 code-unit offsets into the base text
  const firstLineLen = '😀 first'.length; // 8 (emoji counts as 2)
  assert.equal(c.baseStart, firstLineLen + 1);
  assert.equal(c.baseEnd, c.baseStart + 'mid 😀'.length);
  assert.equal(base.slice(c.baseStart, c.baseEnd), 'mid 😀');
  // output offsets land exactly on the conflict block
  assert.ok(r.text.slice(c.start, c.start + '<<<<<<< ours'.length) === '<<<<<<< ours');
  assert.ok(r.text.slice(c.end - '>>>>>>> theirs\n'.length, c.end) === '>>>>>>> theirs\n');
  // merged text still contains the intact emoji (no lone surrogates)
  assert.ok(r.text.includes('😀'));
  assert.ok(!/[\uD800-\uDBFF](?![\uDC00-\uDFFF])/.test(r.text));
  assert.ok(!/(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/.test(r.text));
});

test('UTF-16 offsets: emoji before an auto-merged region stays intact', () => {
  const base = '🎵🎶 head\nbody\ntail\n';
  const ours = '🎵🎶 HEAD\nbody\ntail\n';
  const theirs = '🎵🎶 head\nbody\nTAIL\n';
  const r = merge3(base, ours, theirs);
  assert.equal(r.clean, true);
  assert.equal(r.text, '🎵🎶 HEAD\nbody\nTAIL\n');
});

test('both sides insert different content into an empty base -> conflict', () => {
  const r = merge3('', 'hello\n', 'world\n');
  assert.equal(r.clean, false);
  assert.equal(r.conflicts.length, 1);
  assert.deepEqual(r.conflicts[0].base, []);
  assert.deepEqual(r.conflicts[0].ours, ['hello']);
  assert.deepEqual(r.conflicts[0].theirs, ['world']);
  assert.ok(r.text.includes('hello'));
  assert.ok(r.text.includes('world'));
});

test('insertion at a range boundary does not conflict (declared rule 2)', () => {
  // ours replaces line 1; theirs inserts a new line right after line 1
  const base = 'a\nb\nc\n';
  const ours = 'a\nB\nc\n';
  const theirs = 'a\nb\nNEW\nc\n';
  const r = merge3(base, ours, theirs);
  assert.equal(r.clean, true);
  assert.equal(r.text, 'a\nB\nNEW\nc\n');
});

test('deletion on one side, edit elsewhere on the other auto-merges', () => {
  const base = 'keep\ndrop\nalso-keep\n';
  const ours = 'keep\nalso-keep\n'; // deleted line 1
  const theirs = 'keep\ndrop\nALSO-KEEP\n';
  const r = merge3(base, ours, theirs);
  assert.equal(r.clean, true);
  assert.equal(r.text, 'keep\nALSO-KEEP\n');
});

test('delete vs edit of the same line conflicts without dropping content', () => {
  const base = 'x\nvictim\nz\n';
  const ours = 'x\nz\n'; // deleted 'victim'
  const theirs = 'x\nVICTIM\nz\n'; // edited 'victim'
  const r = merge3(base, ours, theirs);
  assert.equal(r.clean, false);
  const c = r.conflicts[0];
  assert.deepEqual(c.base, ['victim']);
  assert.deepEqual(c.ours, []);
  assert.deepEqual(c.theirs, ['VICTIM']);
  assert.ok(r.text.includes('victim'));
  assert.ok(r.text.includes('VICTIM'));
});

test('computeEdits produces minimal replace/insert/delete edits', () => {
  assert.deepEqual(computeEdits(['a', 'b'], ['a', 'x', 'b']), [
    { start: 1, end: 1, lines: ['x'] },
  ]);
  assert.deepEqual(computeEdits(['a', 'b'], ['a']), [
    { start: 1, end: 2, lines: [] },
  ]);
  assert.deepEqual(computeEdits(['a', 'b'], ['a', 'c']), [
    { start: 1, end: 2, lines: ['c'] },
  ]);
  assert.deepEqual(computeEdits([], []), []);
});
