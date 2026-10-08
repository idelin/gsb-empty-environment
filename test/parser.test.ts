import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseDocument } from '../src/parser.ts';

test('parses headings with levels and positions', () => {
  const doc = parseDocument('# Title\n\n## Section\ntext');
  assert.equal(doc.headings.length, 2);
  assert.deepEqual(doc.headings[0], {
    level: 1,
    text: 'Title',
    line: 0,
    range: { start: { line: 0, character: 0 }, end: { line: 0, character: 7 } },
  });
  assert.equal(doc.headings[1].level, 2);
  assert.equal(doc.headings[1].text, 'Section');
  assert.equal(doc.headings[1].line, 2);
});

test('resolves forward references defined later in the document', () => {
  const doc = parseDocument('See [^a] for details.\n\n[^a]: the definition');
  assert.equal(doc.references.length, 1);
  assert.equal(doc.references[0].name, 'a');
  assert.equal(doc.diagnostics.length, 0);
  assert.equal(doc.definitionMap.get('a')?.line, 2);
  assert.equal(doc.definitionMap.get('a')?.body, 'the definition');
});

test('reports undefined references with exact positions', () => {
  const doc = parseDocument('See [^missing].');
  assert.equal(doc.diagnostics.length, 1);
  const diagnostic = doc.diagnostics[0];
  assert.equal(diagnostic.code, 'undefined-reference');
  assert.equal(diagnostic.severity, 'error');
  assert.deepEqual(diagnostic.range.start, { line: 0, character: 4 });
  assert.deepEqual(diagnostic.range.end, { line: 0, character: 14 });
  assert.equal(doc.diagnosticsByReference.get('missing')?.length, 1);
});

test('duplicate definitions keep the first and report an error', () => {
  const doc = parseDocument('[^a]: first\n[^a]: second\nUse [^a].');
  assert.equal(doc.definitionMap.get('a')?.body, 'first');
  assert.equal(doc.definitions.length, 2);
  assert.equal(doc.duplicates.length, 1);
  assert.equal(doc.duplicates[0].body, 'second');
  const duplicate = doc.diagnostics.find((d) => d.code === 'duplicate-definition');
  assert.ok(duplicate);
  assert.equal(duplicate.range.start.line, 1);
  assert.equal(doc.diagnostics.some((d) => d.code === 'undefined-reference'), false);
});

test('detects circular footnotes across definitions', () => {
  const doc = parseDocument('[^a]: see [^b]\n[^b]: see [^a]\nBody [^a].');
  const cycles = doc.diagnostics.filter((d) => d.code === 'circular-footnote');
  assert.equal(cycles.length, 2);
  assert.deepEqual(cycles[0].relatedNames, ['a', 'b']);
  assert.deepEqual(cycles[1].relatedNames, ['a', 'b']);
  assert.equal(cycles[0].severity, 'warning');
});

test('detects self-referencing footnotes', () => {
  const doc = parseDocument('[^x]: see [^x]');
  const cycles = doc.diagnostics.filter((d) => d.code === 'circular-footnote');
  assert.equal(cycles.length, 1);
  assert.deepEqual(cycles[0].relatedNames, ['x']);
});

test('references inside definition bodies are tracked on both sides', () => {
  const doc = parseDocument('[^a]: see [^b]\n[^b]: done');
  assert.equal(doc.references.length, 1);
  assert.equal(doc.references[0].name, 'b');
  assert.equal(doc.references[0].inDefinitionBody, true);
  assert.equal(doc.definitions[0].references[0], doc.references[0]);
  assert.equal(doc.diagnostics.length, 0);
});

test('reference keys are stable per-name ordinals in document order', () => {
  const doc = parseDocument('[^a] and [^b] and [^a]');
  assert.deepEqual(
    doc.references.map((r) => r.key),
    ['a', 'b', 'a#1'],
  );
});
