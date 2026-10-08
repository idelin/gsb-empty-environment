import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseDocument } from '../src/parser.ts';
import { IncrementalDocument } from '../src/incremental.ts';
import type { ParsedDocument } from '../src/types.ts';

// --- Full-parse oracle, implemented independently of the incremental engine ---

interface RefSignature {
  name: string;
  line: number;
  character: number;
  codes: string;
  target: string;
}

function oracleSignatures(text: string): Map<string, RefSignature> {
  const doc = parseDocument(text);
  const signatures = new Map<string, RefSignature>();
  for (const ref of doc.references) {
    const definition = doc.definitionMap.get(ref.name);
    signatures.set(ref.key, {
      name: ref.name,
      line: ref.line,
      character: ref.range.start.character,
      codes: (doc.diagnosticsByReference.get(ref.key) ?? [])
        .map((d) => d.code)
        .sort()
        .join(','),
      target: definition ? `${definition.name}=${definition.body}` : '',
    });
  }
  return signatures;
}

function oracleAffected(before: string, after: string): string[] {
  const oldSignatures = oracleSignatures(before);
  const newSignatures = oracleSignatures(after);
  const affected: string[] = [];
  for (const [key, next] of newSignatures) {
    const prev = oldSignatures.get(key);
    if (!prev) {
      affected.push(`added:${key}`);
    } else if (prev.codes !== next.codes || prev.target !== next.target) {
      affected.push(`changed:${key}`);
    } else if (prev.line !== next.line || prev.character !== next.character) {
      affected.push(`moved:${key}`);
    }
  }
  for (const key of oldSignatures.keys()) {
    if (!newSignatures.has(key)) affected.push(`removed:${key}`);
  }
  return affected.sort();
}

function actualAffected(doc: IncrementalDocument, after: string): string[] {
  return doc
    .update(after)
    .affectedReferences.map((change) => `${change.kind}:${change.key}`)
    .sort();
}

function assertMatchesFullParse(doc: IncrementalDocument, text: string): ParsedDocument {
  const full = parseDocument(text);
  assert.deepStrictEqual(doc.getDocument(), full);
  return full;
}

// --- Scenarios ---

test('no-op update affects nothing', () => {
  const doc = new IncrementalDocument('See [^a].\n[^a]: def');
  const result = doc.update('See [^a].\n[^a]: def');
  assert.equal(result.affectedReferences.length, 0);
  assert.deepEqual(result.changedLines, { start: 2, oldEnd: 2, newEnd: 2 });
});

test('forward reference is re-diagnosed when its definition appears later', () => {
  const doc = new IncrementalDocument('See [^x].');
  assert.equal(doc.diagnostics[0]?.code, 'undefined-reference');

  const result = doc.update('See [^x].\n\n[^x]: defined now');
  assert.equal(result.diagnostics.length, 0);
  const change = result.affectedReferences.find((c) => c.key === 'x');
  assert.equal(change?.kind, 'changed');
  assert.equal(change?.diagnostics.length, 0);
  assertMatchesFullParse(doc, 'See [^x].\n\n[^x]: defined now');
});

test('deleting a definition re-diagnoses all of its references', () => {
  const before = 'Forward [^a].\n[^a]: def\nMiddle [^a].\n[^b]: see [^a]\nEnd [^a].';
  const after = 'Forward [^a].\nMiddle [^a].\n[^b]: see [^a]\nEnd [^a].';
  const doc = new IncrementalDocument(before);
  assert.equal(doc.diagnostics.length, 0);

  const result = doc.update(after);
  assert.equal(
    result.diagnostics.filter((d) => d.code === 'undefined-reference').length,
    4,
  );
  assert.deepEqual(
    result.affectedReferences.map((c) => `${c.kind}:${c.key}`).sort(),
    ['changed:a', 'changed:a#1', 'changed:a#2', 'changed:a#3'],
  );
  for (const change of result.affectedReferences) {
    assert.equal(change.diagnostics[0]?.code, 'undefined-reference');
  }
  assertMatchesFullParse(doc, after);
});

test('duplicate definitions error and keep the first; removing the duplicate heals', () => {
  const doc = new IncrementalDocument('[^a]: first\nUse [^a].');

  const added = doc.update('[^a]: first\nUse [^a].\n[^a]: second');
  assert.equal(added.diagnostics.filter((d) => d.code === 'duplicate-definition').length, 1);
  assert.equal(doc.getDocument().definitionMap.get('a')?.body, 'first');
  assert.equal(added.affectedReferences.length, 0);

  const removed = doc.update('[^a]: first\nUse [^a].');
  assert.equal(removed.diagnostics.length, 0);
  assert.equal(removed.affectedReferences.length, 0);
});

test('removing the first of two duplicate definitions re-targets references', () => {
  const before = '[^a]: first\nUse [^a].\n[^a]: second';
  const after = 'Use [^a].\n[^a]: second';
  const doc = new IncrementalDocument(before);

  const result = doc.update(after);
  assert.equal(doc.getDocument().definitionMap.get('a')?.body, 'second');
  const change = result.affectedReferences.find((c) => c.key === 'a');
  assert.equal(change?.kind, 'changed');
  assertMatchesFullParse(doc, after);
});

test('editing a definition body can introduce a circular footnote', () => {
  const doc = new IncrementalDocument('[^a]: see [^b]\n[^b]: done\nUse [^a].');
  const result = doc.update('[^a]: see [^b]\n[^b]: see [^a]\nUse [^a].');

  const cycles = result.diagnostics.filter((d) => d.code === 'circular-footnote');
  assert.equal(cycles.length, 2);
  assert.deepEqual(cycles[0].relatedNames, ['a', 'b']);

  const kinds = new Map(result.affectedReferences.map((c) => [c.key, c.kind]));
  assert.equal(kinds.get('a'), 'moved');
  assert.equal(kinds.get('a#1'), 'added');
  assertMatchesFullParse(doc, '[^a]: see [^b]\n[^b]: see [^a]\nUse [^a].');
});

test('inserting lines marks later references as moved with shifted positions', () => {
  const before = '# T\nSee [^a].\n[^a]: def';
  const after = '# T\n\n\nSee [^a].\n[^a]: def';
  const doc = new IncrementalDocument(before);

  const result = doc.update(after);
  assert.deepEqual(result.changedLines, { start: 1, oldEnd: 1, newEnd: 3 });
  const moved = result.affectedReferences.filter((c) => c.kind === 'moved');
  assert.equal(moved.length, 1);
  assert.equal(moved[0].key, 'a');
  assert.equal(moved[0].reference?.line, 3);
  assert.equal(moved[0].previous?.line, 1);

  const full = assertMatchesFullParse(doc, after);
  assert.deepEqual(moved[0].reference?.range, full.references[0].range);
});

test('changed line range is reported for middle edits', () => {
  const doc = new IncrementalDocument('a\nb\nc\nd');
  const result = doc.update('a\nB\nc\nd');
  assert.deepEqual(result.changedLines, { start: 1, oldEnd: 2, newEnd: 2 });
});

// --- Randomized cross-check against full parses ---

function mulberry32(seed: number): () => number {
  let state = seed;
  return () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('incremental updates always match a full reparse (fuzz)', () => {
  const rng = mulberry32(20261008);
  const pool = [
    '# Heading',
    '## Sub heading',
    'plain text line',
    'See [^a] here.',
    'Ref [^b] and [^c].',
    'Again [^a].',
    '[^a]: alpha',
    '[^a]: alpha duplicate',
    '[^b]: beta see [^a]',
    '[^c]: gamma see [^c]',
    '[^d]: delta see [^e]',
    'tail [^d]',
    '',
  ];

  const lines = ['# Doc', 'Intro [^a].', '[^a]: one'];
  const doc = new IncrementalDocument(lines.join('\n'));

  for (let iteration = 0; iteration < 300; iteration++) {
    const before = lines.join('\n');
    const op = Math.floor(rng() * 3);
    const index = Math.floor(rng() * lines.length);
    if (op === 0) {
      lines.splice(index, 0, pool[Math.floor(rng() * pool.length)]);
    } else if (op === 1 && lines.length > 1) {
      lines.splice(index, 1);
    } else {
      lines[index] = pool[Math.floor(rng() * pool.length)];
    }
    const after = lines.join('\n');

    const affected = actualAffected(doc, after);
    assertMatchesFullParse(doc, after);
    assert.deepEqual(
      affected,
      oracleAffected(before, after),
      `affected-set mismatch at iteration ${iteration}`,
    );
  }
});
