import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseDocument } from '../src/index.ts';

test('parses headings with levels and ranges', () => {
  const result = parseDocument('# Title\n\n## Section\n');
  assert.equal(result.headings.length, 2);
  assert.deepEqual(result.headings[0], {
    level: 1,
    text: 'Title',
    range: { start: { line: 0, character: 0 }, end: { line: 0, character: 7 } },
  });
  assert.deepEqual(result.headings[1].level, 2);
  assert.deepEqual(result.headings[1].range.start, { line: 2, character: 0 });
});

test('resolves forward references (definition after reference)', () => {
  const result = parseDocument('See [^later] here.\n\n[^later]: defined below\n');
  assert.equal(result.references.length, 1);
  assert.equal(result.references[0].resolved, true);
  assert.equal(result.references[0].target!.content, 'defined below');
  assert.deepEqual(
    result.diagnostics.map((d) => d.code),
    [],
  );
});

test('reference ranges are exact', () => {
  const result = parseDocument('ab [^x] cd\n');
  assert.deepEqual(result.references[0].range, {
    start: { line: 0, character: 3 },
    end: { line: 0, character: 7 },
  });
  assert.equal(result.references[0].name, 'x');
});

test('duplicate definitions report an error and keep the first', () => {
  const result = parseDocument('[^a]: one\n\n[^a]: two\n\nuse [^a]\n');
  assert.equal(result.definitions.length, 2);
  const duplicates = result.diagnostics.filter((d) => d.code === 'duplicate-definition');
  assert.equal(duplicates.length, 1);
  assert.deepEqual(duplicates[0].range, {
    start: { line: 2, character: 0 },
    end: { line: 2, character: 4 },
  });
  // First definition wins: the reference resolves to 'one'.
  const ref = result.references.find((r) => r.range.start.line === 4)!;
  assert.equal(ref.resolved, true);
  assert.equal(ref.target!.content, 'one');
  assert.equal(ref.target!.range.start.line, 0);
});

test('name matching is case-insensitive and whitespace-collapsed', () => {
  const result = parseDocument('use [^A  B]\n\n[^a b]: def\n');
  assert.equal(result.references[0].resolved, true);
  assert.deepEqual(result.diagnostics, []);
});

test('undefined references are diagnosed', () => {
  const result = parseDocument('missing [^nope] here\n');
  assert.equal(result.references[0].resolved, false);
  const undefinedRefs = result.diagnostics.filter((d) => d.code === 'undefined-reference');
  assert.equal(undefinedRefs.length, 1);
  assert.deepEqual(undefinedRefs[0].range, {
    start: { line: 0, character: 8 },
    end: { line: 0, character: 15 },
  });
});

test('detects a two-definition cycle', () => {
  const result = parseDocument('[^a]: see [^b]\n\n[^b]: and [^a]\n');
  const cycles = result.diagnostics.filter((d) => d.code === 'circular-footnote');
  assert.equal(cycles.length, 2);
  assert.deepEqual(
    cycles.map((d) => d.name).sort(),
    ['a', 'b'],
  );
  // References inside the cycle still resolve; no undefined diagnostics.
  assert.deepEqual(
    result.diagnostics.filter((d) => d.code === 'undefined-reference'),
    [],
  );
});

test('detects a self-referencing footnote', () => {
  const result = parseDocument('[^s]: self [^s]\n');
  const cycles = result.diagnostics.filter((d) => d.code === 'circular-footnote');
  assert.equal(cycles.length, 1);
  assert.equal(cycles[0].name, 's');
});

test('no cycle reported for acyclic chains', () => {
  const result = parseDocument('[^a]: see [^b]\n\n[^b]: see [^c]\n\n[^c]: end\n');
  assert.deepEqual(
    result.diagnostics.filter((d) => d.code === 'circular-footnote'),
    [],
  );
});

test('references inside definition content are collected', () => {
  const result = parseDocument('[^a]: first\n\n[^b]: see [^a] too\n');
  assert.equal(result.references.length, 1);
  assert.equal(result.references[0].name, 'a');
  assert.equal(result.references[0].resolved, true);
  assert.deepEqual(result.references[0].range, {
    start: { line: 2, character: 10 },
    end: { line: 2, character: 14 },
  });
  assert.equal(result.definitions[1].references[0], result.references[0]);
});

test('multi-line definitions with indented continuation', () => {
  const text = '[^a]: line1\n  line2 [^b]\n\n[^b]: x\n';
  const result = parseDocument(text);
  assert.equal(result.definitions.length, 2);
  assert.equal(result.definitions[0].content, 'line1\nline2 [^b]');
  assert.deepEqual(result.definitions[0].range, {
    start: { line: 0, character: 0 },
    end: { line: 1, character: 12 },
  });
  // The [^b] reference sits on the continuation line.
  const inner = result.references.find((r) => r.name === 'b')!;
  assert.deepEqual(inner.range, {
    start: { line: 1, character: 8 },
    end: { line: 1, character: 12 },
  });
  assert.equal(inner.resolved, true);
});

test('unindented line ends a definition', () => {
  const result = parseDocument('[^a]: one\nplain [^b] text\n\n[^b]: two\n');
  assert.equal(result.definitions[0].content, 'one');
  assert.equal(result.definitions[0].references.length, 0);
  const ref = result.references.find((r) => r.name === 'b')!;
  assert.equal(ref.resolved, true);
});

test('duplicate definitions do not shadow cycles of the first', () => {
  // First [^a] is acyclic; the duplicate must not rewire anything.
  const result = parseDocument('[^a]: ok\n\n[^a]: see [^a]\n');
  assert.deepEqual(
    result.diagnostics.map((d) => d.code).sort(),
    ['duplicate-definition'],
  );
});
