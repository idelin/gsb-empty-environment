import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Document, parseDocument } from '../src/index.ts';
import type { ParseResult, Range, TextEdit } from '../src/index.ts';

function edit(startLine: number, startChar: number, endLine: number, endChar: number, newText: string): TextEdit {
  return {
    range: {
      start: { line: startLine, character: startChar },
      end: { line: endLine, character: endChar },
    },
    newText,
  };
}

function applyEditToText(text: string, e: TextEdit): string {
  const starts = [0];
  for (let i = 0; i < text.length; i++) if (text[i] === '\n') starts.push(i + 1);
  const start = starts[e.range.start.line] + e.range.start.character;
  const end = starts[e.range.end.line] + e.range.end.character;
  return text.slice(0, start) + e.newText + text.slice(end);
}

/** Plain comparable snapshot of a parse, used to cross-check with full parse. */
function snapshot(result: ParseResult) {
  return {
    headings: result.headings,
    references: result.references.map((r) => ({
      name: r.name,
      range: r.range,
      resolved: r.resolved,
      targetContent: r.target ? r.target.content : null,
      targetRange: r.target ? r.target.range : null,
    })),
    definitions: result.definitions.map((d) => ({
      name: d.name,
      content: d.content,
      range: d.range,
      nameRange: d.nameRange,
    })),
    diagnostics: result.diagnostics,
  };
}

/** Incremental state must always equal a full re-parse of the same text. */
function assertConsistent(doc: Document) {
  assert.deepEqual(snapshot({
    headings: doc.getHeadings(),
    references: doc.getReferences(),
    definitions: doc.getDefinitions(),
    diagnostics: doc.getDiagnostics(),
  }), snapshot(parseDocument(doc.getText())));
}

function simplified(affected: { change: string; name: string; range: Range }[]) {
  return affected.map((a) => ({ change: a.change, name: a.name, range: a.range }));
}

test('inserting a forward reference reports it as added and resolved', () => {
  const doc = Document.from('[^b]: def b\n');
  const result = doc.applyEdit(edit(0, 0, 0, 0, 'see [^b]\n\n'));
  assert.equal(doc.getText(), 'see [^b]\n\n[^b]: def b\n');
  assert.deepEqual(simplified(result.affectedReferences), [
    {
      change: 'added',
      name: 'b',
      range: { start: { line: 0, character: 4 }, end: { line: 0, character: 8 } },
    },
  ]);
  assert.equal(doc.getReferences()[0].resolved, true);
  assertConsistent(doc);
});

test('deleting a definition re-diagnoses all of its references', () => {
  const doc = Document.from('use [^a] here\n\n[^a]: the def\n\nand [^a] again\n');
  assert.deepEqual(doc.getDiagnostics(), []);
  // Remove the definition line (line 2) together with its newline.
  const result = doc.applyEdit(edit(2, 0, 3, 0, ''));
  assert.equal(doc.getText(), 'use [^a] here\n\n\nand [^a] again\n');
  assert.deepEqual(simplified(result.affectedReferences), [
    {
      change: 'resolution-changed',
      name: 'a',
      range: { start: { line: 0, character: 4 }, end: { line: 0, character: 8 } },
    },
    {
      change: 'resolution-changed',
      name: 'a',
      range: { start: { line: 3, character: 4 }, end: { line: 3, character: 8 } },
    },
  ]);
  const undefinedRefs = result.diagnostics.filter((d) => d.code === 'undefined-reference');
  assert.equal(undefinedRefs.length, 2);
  assertConsistent(doc);
});

test('adding a duplicate definition errors but keeps the first', () => {
  const doc = Document.from('[^a]: one\n\nuse [^a]\n');
  const result = doc.applyEdit(edit(2, 8, 2, 8, '\n\n[^a]: two'));
  const duplicates = result.diagnostics.filter((d) => d.code === 'duplicate-definition');
  assert.equal(duplicates.length, 1);
  assert.deepEqual(duplicates[0].range, {
    start: { line: 4, character: 0 },
    end: { line: 4, character: 4 },
  });
  // No reference changed semantically: still resolved to the first definition.
  assert.deepEqual(result.affectedReferences, []);
  assert.equal(doc.getReferences()[0].target!.content, 'one');
  assertConsistent(doc);
});

test('editing definition content marks its references as target-changed', () => {
  const doc = Document.from('ref [^a]\n\n[^a]: one\n');
  const result = doc.applyEdit(edit(2, 6, 2, 9, 'two'));
  assert.deepEqual(simplified(result.affectedReferences), [
    {
      change: 'target-changed',
      name: 'a',
      range: { start: { line: 0, character: 4 }, end: { line: 0, character: 8 } },
    },
  ]);
  assert.equal(doc.getReferences()[0].target!.content, 'two');
  assertConsistent(doc);
});

test('edits can create and break circular footnotes', () => {
  const doc = Document.from('[^a]: see [^b]\n\n[^b]: end\n');
  assert.deepEqual(doc.getDiagnostics(), []);

  // Create the cycle a -> b -> a.
  const created = doc.applyEdit(edit(2, 6, 2, 9, 'see [^a]'));
  const cycles = created.diagnostics.filter((d) => d.code === 'circular-footnote');
  assert.equal(cycles.length, 2);
  assert.deepEqual(cycles.map((d) => d.name).sort(), ['a', 'b']);
  assert.deepEqual(simplified(created.affectedReferences), [
    {
      change: 'target-changed',
      name: 'b',
      range: { start: { line: 0, character: 10 }, end: { line: 0, character: 14 } },
    },
    {
      change: 'added',
      name: 'a',
      range: { start: { line: 2, character: 10 }, end: { line: 2, character: 14 } },
    },
  ]);
  assertConsistent(doc);

  // Break the cycle again.
  const broken = doc.applyEdit(edit(2, 6, 2, 14, 'end'));
  assert.deepEqual(
    broken.diagnostics.filter((d) => d.code === 'circular-footnote'),
    [],
  );
  assertConsistent(doc);
});

test('pure position shifts keep positions in sync without affected refs', () => {
  const doc = Document.from('intro [^a]\n\n[^a]: x\n');
  const result = doc.applyEdit(edit(0, 0, 0, 0, '# Title\n\n'));
  assert.deepEqual(result.affectedReferences, []);
  assert.deepEqual(doc.getReferences()[0].range, {
    start: { line: 2, character: 6 },
    end: { line: 2, character: 10 },
  });
  assert.deepEqual(doc.getDefinitions()[0].range.start, { line: 4, character: 0 });
  assertConsistent(doc);
});

test('removing a reference reports it as removed', () => {
  const doc = Document.from('a [^x] b\n\n[^x]: def\n');
  const result = doc.applyEdit(edit(0, 2, 0, 6, ''));
  assert.equal(doc.getText(), 'a  b\n\n[^x]: def\n');
  assert.deepEqual(simplified(result.affectedReferences), [
    {
      change: 'removed',
      name: 'x',
      range: { start: { line: 0, character: 2 }, end: { line: 0, character: 6 } },
    },
  ]);
  assertConsistent(doc);
});

test('editing a continuation line updates the definition', () => {
  const doc = Document.from('use [^a]\n\n[^a]: line1\n  line2\n');
  const result = doc.applyEdit(edit(3, 2, 3, 7, 'changed [^b]'));
  assert.equal(doc.getDefinitions()[0].content, 'line1\nchanged [^b]');
  // New undefined reference [^b] appeared inside the definition.
  assert.deepEqual(simplified(result.affectedReferences), [
    {
      change: 'target-changed',
      name: 'a',
      range: { start: { line: 0, character: 4 }, end: { line: 0, character: 8 } },
    },
    {
      change: 'added',
      name: 'b',
      range: { start: { line: 3, character: 10 }, end: { line: 3, character: 14 } },
    },
  ]);
  assertConsistent(doc);
});

test('out-of-bounds edits are rejected', () => {
  const doc = Document.from('abc\n');
  assert.throws(() => doc.applyEdit(edit(5, 0, 5, 1, 'x')), RangeError);
});

// --- Randomized cross-check against full parse -----------------------------

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const SNIPPETS = [
  'text ',
  '[^a]',
  ' [^b] ',
  '[^c]',
  '\n',
  '\n\n',
  '# Head\n\n',
  '[^a]: redefined\n\n',
  '[^b]: see [^a]\n',
  '\n  cont [^c]\n',
  '## Sub\n',
  '[^x]: brand new\n\nuse [^x]\n',
];

function randomDocument(rand: () => number): string {
  const names = ['a', 'b', 'c'];
  const parts: string[] = [];
  const blocks = 2 + Math.floor(rand() * 4);
  for (let i = 0; i < blocks; i++) {
    const kind = rand();
    if (kind < 0.3) {
      parts.push(`# H${i}\n`);
    } else if (kind < 0.6) {
      const name = names[Math.floor(rand() * names.length)];
      parts.push(`line ${i} [^${name}] end\n`);
    } else {
      const name = names[Math.floor(rand() * names.length)];
      const other = names[Math.floor(rand() * names.length)];
      parts.push(`[^${name}]: def ${i} [^${other}]\n`);
    }
    if (rand() < 0.8) parts.push('\n');
  }
  return parts.join('');
}

test('fuzz: incremental updates always match a full re-parse', () => {
  const rand = mulberry32(20261008);
  for (let iteration = 0; iteration < 300; iteration++) {
    const doc = Document.from(randomDocument(rand));
    assertConsistent(doc);
    const edits = 1 + Math.floor(rand() * 4);
    for (let e = 0; e < edits; e++) {
      const text = doc.getText();
      const lines = text.split('\n');
      const startLine = Math.floor(rand() * lines.length);
      const startChar = Math.floor(rand() * (lines[startLine].length + 1));
      let endLine = startLine;
      let endChar = startChar;
      if (rand() < 0.5) {
        endLine = Math.min(lines.length - 1, startLine + Math.floor(rand() * 3));
        endChar = Math.floor(rand() * (lines[endLine].length + 1));
        if (endLine === startLine && endChar < startChar) endChar = startChar;
      }
      const theEdit = edit(
        startLine,
        startChar,
        endLine,
        endChar,
        SNIPPETS[Math.floor(rand() * SNIPPETS.length)],
      );
      const expectedText = applyEditToText(text, theEdit);
      const result = doc.applyEdit(theEdit);
      assert.equal(doc.getText(), expectedText, `text mismatch at iteration ${iteration}`);
      assertConsistent(doc);
      for (const affected of result.affectedReferences) {
        assert.match(affected.change, /^(added|removed|resolution-changed|target-changed)$/);
        assert.ok(affected.range.start.line <= affected.range.end.line);
      }
    }
  }
});
