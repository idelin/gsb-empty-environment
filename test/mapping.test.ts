import assert from 'node:assert/strict';
import test from 'node:test';
import { applyEdit, type Edit } from '../src/edits.ts';
import { History } from '../src/history.ts';
import {
  composeMappings,
  identityMapping,
  invertMapping,
  mapOffset,
  mapRange,
  mappingFromEdit,
  type SegmentMapping,
} from '../src/mapping.ts';
import { SurrogatePairError } from '../src/utf16.ts';
import { referenceMapRange } from './helpers.ts';

function composedOf(text: string, edits: Edit[]): SegmentMapping {
  let mapping = identityMapping(text.length);
  let len = text.length;
  for (const e of edits) {
    const m = mappingFromEdit(len, e);
    mapping = composeMappings(mapping, m);
    len = m.newLen;
  }
  return mapping;
}

function stepwiseMap(text: string, edits: Edit[], start: number, end: number) {
  let cur = { start, end };
  let len = text.length;
  let deletedUnits = 0;
  for (const e of edits) {
    const m = mappingFromEdit(len, e);
    const r = mapRange(m, cur.start, cur.end);
    if (r.status === 'deleted') return r;
    deletedUnits += r.deletedUnits;
    cur = { start: r.start, end: r.end };
    len = m.newLen;
  }
  return { status: (deletedUnits > 0 ? 'shrank' : 'intact') as 'shrank' | 'intact', ...cur, deletedUnits };
}

function sampleRanges(len: number): Array<[number, number]> {
  const out: Array<[number, number]> = [];
  for (let s = 0; s <= len; s += 2) {
    for (let e = s + 1; e <= Math.min(len, s + 10); e += 2) out.push([s, e]);
  }
  return out;
}

function buildEdits(initial: string, script: Array<(text: string, id: number) => Edit>): { text: string; edits: Edit[] } {
  let text = initial;
  const edits: Edit[] = [];
  let id = 0;
  for (const make of script) {
    const e = make(text, ++id);
    text = applyEdit(text, e);
    edits.push(e);
  }
  return { text, edits };
}

function assertMappingMatchesReference(initial: string, edits: Edit[]): void {
  const composed = composedOf(initial, edits);
  for (const [s, e] of sampleRanges(initial.length)) {
    const expected = referenceMapRange(initial, edits, s, e);
    const viaComposed = mapRange(composed, s, e);
    const viaStepwise = stepwiseMap(initial, edits, s, e);
    assert.deepEqual(
      { status: viaComposed.status, start: viaComposed.start, end: viaComposed.end },
      { status: expected.status, start: expected.start, end: expected.end },
      `composed mapping mismatch for [${s}, ${e})`,
    );
    assert.deepEqual(
      { status: viaStepwise.status, start: viaStepwise.start, end: viaStepwise.end },
      { status: expected.status, start: expected.start, end: expected.end },
      `stepwise mapping mismatch for [${s}, ${e})`,
    );
  }
}

test('composed mapping matches stepwise reference on mixed edits (LF)', () => {
  const initial = 'alpha\nbeta\ngamma\ndelta\n';
  const { edits } = buildEdits(initial, [
    () => ({ kind: 'insert', id: 0, offset: 0, text: '// header\n' }),
    (t) => ({ kind: 'delete', id: 0, start: t.indexOf('beta'), end: t.indexOf('beta') + 5 }),
    (t) => ({ kind: 'insert', id: 0, offset: t.indexOf('delta'), text: '>> ' }),
  ]);
  assertMappingMatchesReference(initial, edits);
});

test('composed mapping matches stepwise reference on mixed edits (CRLF)', () => {
  const initial = 'alpha\r\nbeta\r\ngamma\r\ndelta\r\n';
  const { edits } = buildEdits(initial, [
    () => ({ kind: 'insert', id: 0, offset: 0, text: '// header\r\n' }),
    (t) => ({ kind: 'delete', id: 0, start: t.indexOf('beta'), end: t.indexOf('beta') + 6 }),
    (t) => ({ kind: 'insert', id: 0, offset: t.indexOf('delta'), text: '>> ' }),
  ]);
  assertMappingMatchesReference(initial, edits);
});

test('duplicate text: edits around repeated fragments map correctly', () => {
  const initial = 'foo x foo y foo z foo';
  const { edits } = buildEdits(initial, [
    (t) => ({ kind: 'insert', id: 0, offset: t.indexOf('foo', 6), text: 'bar ' }),
    (t) => ({ kind: 'delete', id: 0, start: t.lastIndexOf('foo'), end: t.lastIndexOf('foo') + 3 }),
    (t) => ({ kind: 'insert', id: 0, offset: t.indexOf('foo'), text: 'q' }),
  ]);
  assertMappingMatchesReference(initial, edits);
});

test('whole-paragraph move: identity follows the moved block', () => {
  const initial = 'p1-line\np2-line\np3-line\n';
  const { text, edits } = buildEdits(initial, [
    (t) => ({
      kind: 'move',
      id: 0,
      start: t.indexOf('p2-line\n'),
      end: t.indexOf('p2-line\n') + 8,
      to: t.length - 8,
      stableId: 'para-p2',
    }),
  ]);
  assert.equal(text, 'p1-line\np3-line\np2-line\n');
  assertMappingMatchesReference(initial, edits);
  const composed = composedOf(initial, edits);
  const inside = mapRange(composed, initial.indexOf('p2'), initial.indexOf('p2') + 2);
  assert.deepEqual(
    { status: inside.status, start: inside.start, end: inside.end },
    { status: 'intact', start: text.indexOf('p2'), end: text.indexOf('p2') + 2 },
  );
});

test('move right past another paragraph matches reference', () => {
  const initial = 'one\ntwo\nthree\nfour\n';
  const { edits } = buildEdits(initial, [
    (t) => ({
      kind: 'move',
      id: 0,
      start: t.indexOf('two\n'),
      end: t.indexOf('two\n') + 4,
      to: t.indexOf('four\n'),
      stableId: 'para-two',
    }),
    (t) => ({ kind: 'insert', id: 0, offset: t.indexOf('three'), text: '! ' }),
  ]);
  assertMappingMatchesReference(initial, edits);
});

test('branch merge: composed mapping across merge matches stepwise and token reference', () => {
  const h = new History('aaa bbb ccc');
  const v0 = h.current;
  h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset: 0, text: 'A:' });
  const mainTip = h.current;
  h.checkout(v0);
  h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset: 11, text: ':B' });
  const branchTip = h.current;
  h.checkout(mainTip);
  h.merge(branchTip);
  assert.equal(h.text(), 'A:aaa bbb ccc:B');

  const path = h.mappingBetween(v0, h.current)!;
  assert.ok(path);
  const forwardEdits: Edit[] = path.steps
    .filter((s) => s.direction === 'forward' && s.version.edit)
    .map((s) => s.version.edit!);
  for (const [s, e] of sampleRanges('aaa bbb ccc'.length)) {
    const viaComposed = mapRange(path.mapping, s, e);
    const expected = referenceMapRange('aaa bbb ccc', forwardEdits, s, e);
    assert.deepEqual(
      { status: viaComposed.status, start: viaComposed.start, end: viaComposed.end },
      { status: expected.status, start: expected.start, end: expected.end },
      `merge mapping mismatch for [${s}, ${e})`,
    );
  }
});

test('undo branch: mapping composes inverse edits (stepwise equals composed)', () => {
  const h = new History('hello world');
  h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset: 0, text: 'say ' });
  const v1 = h.current;
  h.undo();
  h.applyEdit({ kind: 'insert', id: h.nextEditId(), offset: 5, text: '!' });
  const path = h.mappingBetween(v1, h.current)!;
  assert.ok(path);
  assert.deepEqual(path.steps.map((s) => s.direction), ['undo', 'forward']);
  const stepwise = (() => {
    const inv = invertMapping(h.get(v1).mapping!);
    const a = mapRange(inv, 10, 15);
    const b = mapRange(h.get(h.current).mapping!, a.start, a.end);
    return b;
  })();
  const composed = mapRange(path.mapping, 10, 15);
  assert.deepEqual(
    { status: composed.status, start: composed.start, end: composed.end },
    { status: stepwise.status, start: stepwise.start, end: stepwise.end },
  );
  assert.deepEqual(
    { start: composed.start, end: composed.end },
    { start: 7, end: 12 },
  );
});

test('surrogate pairs cannot be split by edits', () => {
  const text = 'a\u{1F600}b';
  assert.equal(text.length, 4);
  assert.throws(
    () => applyEdit(text, { kind: 'insert', id: 1, offset: 2, text: 'x' }),
    SurrogatePairError,
  );
  assert.throws(
    () => applyEdit(text, { kind: 'delete', id: 2, start: 1, end: 2 }),
    SurrogatePairError,
  );
  assert.throws(
    () => applyEdit(text, { kind: 'move', id: 3, start: 0, end: 1, to: 1, stableId: 'm' }),
    SurrogatePairError,
  );
});

test('UTF-16 offsets: emoji counts as 2 units and shifts stay exact', () => {
  const initial = 'a\u{1F600}b tail';
  const { edits } = buildEdits(initial, [
    () => ({ kind: 'insert', id: 0, offset: 0, text: 'pre ' }),
    (t) => ({ kind: 'delete', id: 0, start: t.indexOf('b '), end: t.indexOf('b ') + 2 }),
  ]);
  assertMappingMatchesReference(initial, edits);
  const composed = composedOf(initial, edits);
  const emoji = mapRange(composed, 1, 3);
  assert.deepEqual({ status: emoji.status, start: emoji.start, end: emoji.end }, { status: 'intact', start: 5, end: 7 });
});

test('mapOffset affinity around an insertion point', () => {
  const m = mappingFromEdit(5, { kind: 'insert', id: 1, offset: 2, text: 'XY' });
  assert.equal(mapOffset(m, 2, 'left'), 2);
  assert.equal(mapOffset(m, 2, 'right'), 4);
  assert.equal(mapOffset(m, 0), 0);
  assert.equal(mapOffset(m, 5, 'left'), 7);
});

test('deleted region maps to null offset and deleted range', () => {
  const m = mappingFromEdit(10, { kind: 'delete', id: 1, start: 3, end: 6 });
  assert.equal(mapOffset(m, 4), null);
  const r = mapRange(m, 2, 8);
  assert.equal(r.status, 'shrank');
  assert.deepEqual({ start: r.start, end: r.end }, { start: 2, end: 5 });
  assert.equal(mapRange(m, 3, 6).status, 'deleted');
});
