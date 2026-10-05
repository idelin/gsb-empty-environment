import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  IncrementalDocument,
  parseFull,
  serialize,
  type SyntaxNode,
} from '../src/index.ts';

function occOf(doc: IncrementalDocument, text: string, nth = 0) {
  const matches = doc.occurrences.filter((o) => o.node.text === text);
  assert.ok(matches.length > nth, `expected occurrence of ${text}`);
  return matches[nth];
}

test('basic blocks, blank lines and comments', () => {
  const doc = new IncrementalDocument([
    '# header comment',
    '',
    'alpha',
    '  a1',
    '',
    '  a2 # trailing',
    'beta',
  ].join('\n'));

  assert.equal(doc.errors.length, 0);
  assert.deepEqual(
    doc.occurrences.map((o) => [o.node.text, o.depth, o.startLine]),
    [
      ['alpha', 0, 2],
      ['a1', 1, 3],
      ['a2 # trailing', 1, 5],
      ['beta', 0, 6],
    ],
  );
  assert.equal(occOf(doc, 'alpha').endLine, 5);
});

test('tab indentation reports an error but the rest of the file survives', () => {
  const doc = new IncrementalDocument('foo\n\tbar\nbaz');

  assert.equal(doc.errors.length, 1);
  assert.equal(doc.errors[0].kind, 'tab');
  assert.equal(doc.errors[0].line, 1);
  // Recovery: tab counts as 4 columns, so bar nests under foo; baz is fine.
  assert.deepEqual(doc.occurrences.map((o) => o.node.text), ['foo', 'bar', 'baz']);
  assert.equal(occOf(doc, 'bar').depth, 1);
  assert.equal(occOf(doc, 'baz').depth, 0);
});

test('brackets continue a statement and ignore indentation inside', () => {
  const doc = new IncrementalDocument([
    'result = call(',
    '',
    '      arg1,',
    ' arg2',
    ')',
    'next',
  ].join('\n'));

  assert.equal(doc.errors.length, 0);
  assert.equal(doc.occurrences.length, 2);
  const call = occOf(doc, 'result = call(');
  assert.deepEqual(
    call.node.continuations.map((c) => c.text),
    ['arg1,', 'arg2', ')'],
  );
  assert.equal(occOf(doc, 'next').depth, 0);
});

test('inconsistent dedent is reported and recovered', () => {
  const doc = new IncrementalDocument('a\n  b\n c');

  assert.ok(doc.errors.some((e) => e.kind === 'dedent' && e.line === 2));
  // Recovered: c attaches to the nearest shallower ancestor (a).
  assert.equal(occOf(doc, 'c').depth, 1);
  assert.equal(doc.occurrences.length, 3);
});

test('insert blank line: full reuse, minimal affected range, matches full parse', () => {
  const doc = new IncrementalDocument('alpha\n  a1\nbeta\n  b1');
  const rootBefore = doc.root;

  const report = doc.applyEdit(2, 0, ['']);
  const full = parseFull('alpha\n  a1\n\nbeta\n  b1');

  assert.equal(serialize(doc.root), serialize(full.root));
  assert.deepEqual(doc.errors, full.errors);
  assert.equal(doc.root, rootBefore, 'root object itself is reused');
  assert.equal(report.reusedNodes, report.totalNodes);
  assert.deepEqual(report.affected, { startLine: 2, endLine: 2 });
  assert.deepEqual(report.relexed, { startLine: 2, endLine: 2 });
});

test('delete closing bracket: error recovery, incremental matches full parse', () => {
  const doc = new IncrementalDocument('foo(\n  a,\n  b\n)\nbar\n  baz');
  assert.equal(doc.errors.length, 0);

  const report = doc.applyEdit(3, 1, []); // delete the ")" line
  const full = parseFull('foo(\n  a,\n  b\nbar\n  baz');

  // Incremental result is identical to a from-scratch parse.
  assert.equal(serialize(doc.root), serialize(full.root));
  assert.deepEqual(doc.errors, full.errors);

  // The unclosed bracket is reported, but nothing after it disappears:
  // bar and baz are kept as continuation lines of foo.
  assert.ok(doc.errors.some((e) => e.kind === 'unclosed' && e.line === 0));
  assert.equal(doc.occurrences.length, 1);
  const foo = occOf(doc, 'foo(');
  assert.deepEqual(
    foo.node.continuations.map((c) => c.text),
    ['a,', 'b', 'bar', 'baz'],
  );

  // Re-lexing had to propagate to EOF (bracket state never re-synced).
  assert.equal(report.relexed.endLine, 4);
  assert.deepEqual(report.affected, { startLine: 0, endLine: 4 });
});

test('cross-block paste reuses the identical subtree objects', () => {
  const doc = new IncrementalDocument('alpha\n  a1\n  a2\nbeta\n  b1');
  const alphaBefore = occOf(doc, 'alpha').node;
  const betaBefore = occOf(doc, 'beta').node;

  const report = doc.applyEdit(5, 0, ['alpha', '  a1', '  a2']);
  const full = parseFull('alpha\n  a1\n  a2\nbeta\n  b1\nalpha\n  a1\n  a2');

  assert.equal(serialize(doc.root), serialize(full.root));
  assert.deepEqual(doc.errors, full.errors);

  const alphas = doc.occurrences.filter((o) => o.node.text === 'alpha');
  assert.equal(alphas.length, 2);
  assert.equal(alphas[0].node, alphaBefore, 'original block untouched');
  assert.equal(alphas[1].node, alphaBefore, 'pasted copy is the same object');
  assert.equal(occOf(doc, 'beta').node, betaBefore, 'unrelated block reused');

  // Only the root is new; every subtree occurrence resolves to old objects.
  assert.equal(report.totalNodes, 9);
  assert.equal(report.reusedNodes, 8);
  assert.deepEqual(report.affected, { startLine: 5, endLine: 7 });
});

test('edit a line: ancestors change, sibling subtrees are reused', () => {
  const doc = new IncrementalDocument('alpha\n  a1\n  a2\nbeta');
  const a2Before = occOf(doc, 'a2').node;
  const betaBefore = occOf(doc, 'beta').node;

  const report = doc.applyEdit(1, 1, ['  a1x']);
  const full = parseFull('alpha\n  a1x\n  a2\nbeta');

  assert.equal(serialize(doc.root), serialize(full.root));
  assert.equal(occOf(doc, 'a2').node, a2Before);
  assert.equal(occOf(doc, 'beta').node, betaBefore);
  // The alpha block (lines 0-2) changed because a descendant changed.
  assert.deepEqual(report.affected, { startLine: 0, endLine: 2 });
});

test('node ids are stable across unrelated edits', () => {
  const doc = new IncrementalDocument('alpha\n  a1\nbeta\n  b1');
  const idsBefore = new Map(doc.occurrences.map((o) => [o.node.text, o.node.id]));

  doc.applyEdit(0, 1, ['alpha']); // retype identical head line

  for (const [text, id] of idsBefore) {
    assert.equal(occOf(doc, text).node.id, id, `id of ${text} is stable`);
  }
});

test('unmatched closing bracket is an error, parsing continues', () => {
  const doc = new IncrementalDocument('a\n)\nb');
  assert.ok(doc.errors.some((e) => e.kind === 'bracket' && e.line === 1));
  assert.deepEqual(doc.occurrences.map((o) => o.node.text), ['a', ')', 'b']);
});
