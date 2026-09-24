import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  FoldedTextProjection,
  FoldIntersectionError,
  SurrogatePairError,
  FoldRangeError,
  splitsSurrogatePair,
} from '../src/folding.ts';
import { positionAt, offsetAt } from '../src/position.ts';

const NESTED = 'class A {\n  method() {\n    body();\n  }\n}\nafter';

function nestedSpec() {
  const classOpen = NESTED.indexOf('{');
  const classClose = NESTED.lastIndexOf('}');
  const methodOpen = NESTED.indexOf('{', classOpen + 1);
  const methodClose = NESTED.indexOf('}', methodOpen);
  return {
    outer: { start: classOpen + 1, end: classClose },
    inner: { start: methodOpen + 1, end: methodClose },
  };
}

test('single fold projects to placeholder and maps offsets both ways', () => {
  const text = 'foo BAR baz';
  const p = new FoldedTextProjection(text, [{ start: 4, end: 7 }]);
  assert.equal(p.getVisibleText(), 'foo … baz');

  // Original offsets outside the fold round-trip exactly.
  assert.equal(p.toVisibleOffset(0), 0);
  assert.equal(p.toVisibleOffset(4), 4); // fold start -> placeholder start
  assert.equal(p.toVisibleOffset(7), 5); // fold end -> placeholder end
  assert.equal(p.toVisibleOffset(11), 9);
  assert.equal(p.toOriginalOffset(0), 0);
  assert.equal(p.toOriginalOffset(4), 4);
  assert.equal(p.toOriginalOffset(5), 7);
  assert.equal(p.toOriginalOffset(9), 11);

  // Offsets inside the folded region snap to the nearest placeholder edge.
  assert.equal(p.toVisibleOffset(5), 4);
  assert.equal(p.toVisibleOffset(6), 5);
});

test('nested folds: outer collapse hides inner placeholder; expanding outer keeps inner collapsed', () => {
  const { outer, inner } = nestedSpec();
  const p = new FoldedTextProjection(NESTED, [outer, inner]);

  // Both collapsed: only the outer placeholder is visible.
  assert.equal(p.getVisibleText(), 'class A {…}\nafter');

  const folds = p.getFolds();
  assert.equal(folds.length, 2);
  const [outerState, innerState] = folds;
  assert.equal(outerState.depth, 0);
  assert.equal(innerState.depth, 1);
  assert.ok(outerState.collapsed);
  assert.ok(innerState.collapsed);

  // Expand the outer fold: inner fold is still collapsed and shows its own placeholder.
  p.setCollapsed(outerState.id, false);
  assert.equal(p.getVisibleText(), 'class A {\n  method() {…}\n}\nafter');

  // Re-collapse the outer fold: the inner placeholder is hidden again.
  p.setCollapsed(outerState.id, true);
  assert.equal(p.getVisibleText(), 'class A {…}\nafter');
  // Expand both: the full original text is restored.
  p.setCollapsed(outerState.id, false);
  p.setCollapsed(innerState.id, false);
  assert.equal(p.getVisibleText(), NESTED);
});

test('disjoint and touching folds are accepted', () => {
  const text = 'aaBBccDDee';
  const p = new FoldedTextProjection(text, [
    { start: 2, end: 4, placeholder: '<1>' },
    { start: 6, end: 8, placeholder: '<2>' },
  ]);
  assert.equal(p.getVisibleText(), 'aa<1>cc<2>ee');

  const touching = new FoldedTextProjection(text, [
    { start: 0, end: 2 },
    { start: 2, end: 4 },
  ]);
  assert.equal(touching.getVisibleText(), '……ccDDee');
});

test('partially intersecting folds are rejected by default', () => {
  assert.throws(
    () => new FoldedTextProjection('0123456789', [{ start: 2, end: 6 }, { start: 4, end: 8 }]),
    FoldIntersectionError,
  );
  // Also rejected when discovered through a chain of nests.
  assert.throws(
    () =>
      new FoldedTextProjection('0123456789abcdef', [
        { start: 0, end: 10 },
        { start: 2, end: 4 },
        { start: 3, end: 12 },
      ]),
    FoldIntersectionError,
  );
});

test('partially intersecting folds can be normalized by merging', () => {
  const p = new FoldedTextProjection('0123456789', [{ start: 2, end: 6 }, { start: 4, end: 8 }], {
    onIntersection: 'merge',
  });
  assert.equal(p.getFolds().length, 1);
  assert.deepEqual([p.getFolds()[0].start, p.getFolds()[0].end], [2, 8]);
  assert.equal(p.getVisibleText(), '01…89');

  // A merge that swallows a nested fold keeps it nested inside the union.
  const q = new FoldedTextProjection(
    '0123456789abcdef',
    [{ start: 0, end: 10 }, { start: 2, end: 4 }, { start: 6, end: 14 }],
    { onIntersection: 'merge' },
  );
  const folds = q.getFolds();
  assert.equal(folds.length, 2);
  assert.deepEqual([folds[0].start, folds[0].end, folds[0].depth], [0, 14, 0]);
  assert.deepEqual([folds[1].start, folds[1].end, folds[1].depth], [2, 4, 1]);
});

test('invalid ranges are rejected', () => {
  assert.throws(() => new FoldedTextProjection('abc', [{ start: 2, end: 2 }]), FoldRangeError);
  assert.throws(() => new FoldedTextProjection('abc', [{ start: -1, end: 2 }]), FoldRangeError);
  assert.throws(() => new FoldedTextProjection('abc', [{ start: 0, end: 4 }]), FoldRangeError);
  assert.throws(() => new FoldedTextProjection('abc', [{ start: 1.5, end: 2 }]), FoldRangeError);
});

test('UTF-16: emoji counts as two code units and folds may not split surrogate pairs', () => {
  const text = 'a😀b😀c'; // UTF-16 length 7
  assert.equal(text.length, 7);

  const p = new FoldedTextProjection(text, [{ start: 1, end: 3 }]); // hides "😀"
  assert.equal(p.getVisibleText(), 'a…b😀c');
  assert.equal(p.toVisibleOffset(3), 2);
  assert.equal(p.toOriginalOffset(2), 3);

  assert.throws(() => new FoldedTextProjection(text, [{ start: 2, end: 3 }]), SurrogatePairError);
  assert.throws(() => new FoldedTextProjection(text, [{ start: 1, end: 2 }]), SurrogatePairError);
  assert.ok(splitsSurrogatePair(text, 2));
  assert.ok(!splitsSurrogatePair(text, 1));
  assert.ok(!splitsSurrogatePair(text, 3));
});

test('UTF-16: edits may not split surrogate pairs in the visible text', () => {
  const p = new FoldedTextProjection('x😀y', []);
  assert.throws(() => p.applyEdit(2, 2, 'z'), SurrogatePairError);
  p.applyEdit(1, 1, 'Q'); // before the emoji is fine
  assert.equal(p.getOriginalText(), 'xQ😀y');
});

test('selection spanning a placeholder maps back to the full original range', () => {
  const text = 'foo BAR baz';
  const p = new FoldedTextProjection(text, [{ start: 4, end: 7, placeholder: '···' }]);
  assert.equal(p.getVisibleText(), 'foo ··· baz');

  // Selection exactly over the placeholder.
  assert.deepEqual(p.mapVisibleRangeToOriginal(4, 7), { start: 4, end: 7 });
  // Selection strictly inside the placeholder expands to the whole fold.
  assert.deepEqual(p.mapVisibleRangeToOriginal(5, 6), { start: 4, end: 7 });
  // Selection crossing the placeholder from both sides.
  assert.deepEqual(p.mapVisibleRangeToOriginal(2, 9), { start: 2, end: 9 });
  // Selection ending exactly at the placeholder start does not include the fold.
  assert.deepEqual(p.mapVisibleRangeToOriginal(1, 4), { start: 1, end: 4 });
});

test('editing before a fold shifts the placeholder and preserves nesting', () => {
  const { outer, inner } = nestedSpec();
  const p = new FoldedTextProjection(NESTED, [outer, inner]);
  p.setCollapsed(p.getFolds()[0].id, false); // expand outer so inner placeholder is visible
  const before = p.getVisibleText();
  assert.equal(before, 'class A {\n  method() {…}\n}\nafter');

  p.applyEdit(0, 0, '/*c*/'); // insert 5 chars at the very start
  assert.equal(p.getVisibleText(), '/*c*/class A {\n  method() {…}\n}\nafter');

  const folds = p.getFolds();
  assert.equal(folds.length, 2);
  assert.deepEqual([folds[0].start, folds[0].end], [outer.start + 5, outer.end + 5]);
  assert.deepEqual([folds[1].start, folds[1].end], [inner.start + 5, inner.end + 5]);
  assert.equal(folds[1].depth, 1); // still nested
  assert.ok(folds[1].collapsed); // inner still collapsed
});

test('editing folded content replaces it and keeps the fold around the new content', () => {
  const { outer, inner } = nestedSpec();
  const p = new FoldedTextProjection(NESTED, [outer, inner]);
  const outerId = p.getFolds()[0].id;
  p.setCollapsed(outerId, false);

  // Select the inner placeholder exactly and type over it.
  const visible = p.getVisibleText();
  const phStart = visible.indexOf('…');
  p.applyEdit(phStart, phStart + 1, 'X');

  // The fold survives around the new content and stays collapsed.
  assert.equal(p.getVisibleText(), 'class A {\n  method() {…}\n}\nafter');
  assert.equal(p.getOriginalText(), 'class A {\n  method() {X}\n}\nafter');
  const folds = p.getFolds();
  assert.equal(folds.length, 2);
  const innerState = folds[1];
  assert.equal(innerState.end - innerState.start, 1); // fold now wraps "X"
  assert.equal(p.getOriginalText().slice(innerState.start, innerState.end), 'X');
  assert.equal(innerState.depth, 1);
});

test('deleting across a placeholder removes the fold but keeps its siblings', () => {
  const text = 'ab[inner]cd';
  const p = new FoldedTextProjection(text, [{ start: 2, end: 9 }]);
  assert.equal(p.getVisibleText(), 'ab…cd');

  // Delete from before the placeholder into the text after it.
  p.applyEdit(1, 4, '');
  assert.equal(p.getOriginalText(), 'ad');
  assert.equal(p.getVisibleText(), 'ad');
  assert.equal(p.getFolds().length, 0);
});

test('insertion inside an expanded fold grows it; folds after the edit shift', () => {
  const text = 'ab[inner]cd{last}ef';
  const p = new FoldedTextProjection(text, [
    { start: 2, end: 9, collapsed: false },
    { start: 11, end: 17 },
  ]);
  assert.equal(p.getVisibleText(), 'ab[inner]cd…ef');

  // Insert "ZZ" at original offset 5 (inside the first, expanded fold) => visible offset 5.
  p.applyEdit(5, 5, 'ZZ');
  assert.equal(p.getOriginalText(), 'ab[inZZner]cd{last}ef');
  const folds = p.getFolds();
  assert.deepEqual([folds[0].start, folds[0].end], [2, 11]); // grew by 2
  assert.deepEqual([folds[1].start, folds[1].end], [13, 19]); // shifted by 2
  assert.equal(p.getVisibleText(), 'ab[inZZner]cd…ef');
});

test('LF and CRLF line endings: position/offset conversion', () => {
  const lf = 'line1\nline2\nline3';
  const crlf = 'line1\r\nline2\r\nline3';
  const mixed = 'a\nb\r\nc\rd';

  assert.deepEqual(positionAt(lf, 6), { line: 1, character: 0 });
  assert.deepEqual(positionAt(crlf, 7), { line: 1, character: 0 });
  assert.equal(offsetAt(lf, { line: 2, character: 0 }), 12);
  assert.equal(offsetAt(crlf, { line: 2, character: 0 }), 14);

  // Offset between \r and \n belongs to the end of the previous line.
  assert.deepEqual(positionAt(crlf, 6), { line: 0, character: 6 });

  // Mixed endings round-trip.
  for (const offset of [0, 1, 2, 3, 4, 5, 6, 7, 8]) {
    if (offset === 4) continue; // between \r and \n: not a stable position
    assert.equal(offsetAt(mixed, positionAt(mixed, offset)), offset, `offset ${offset}`);
  }
  assert.deepEqual(positionAt(mixed, 8), { line: 3, character: 1 });

  // Character offsets past the line end clamp to the line end.
  assert.equal(offsetAt(lf, { line: 0, character: 99 }), 5);
});

test('folds spanning CRLF lines project and map correctly', () => {
  const text = 'line1\r\nline2\r\nline3';
  const start = text.indexOf('\r\n'); // 5
  const end = text.indexOf('\r\n', start + 2); // 12
  const p = new FoldedTextProjection(text, [{ start, end }]); // hides "\r\nline2"
  assert.equal(p.getVisibleText(), 'line1…\r\nline3');

  // The visible line break after the placeholder maps back across the fold.
  const visibleBreak = p.getVisibleText().indexOf('\r\n');
  assert.deepEqual(positionAt(p.getVisibleText(), visibleBreak + 2), { line: 1, character: 0 });
  assert.equal(p.toOriginalOffset(visibleBreak + 2), end + 2);
  assert.equal(p.toVisibleOffset(end + 2), visibleBreak + 2);
});

test('folding the entire text yields a single placeholder', () => {
  const p = new FoldedTextProjection('everything', [{ start: 0, end: 10 }]);
  assert.equal(p.getVisibleText(), '…');
  assert.equal(p.toVisibleOffset(0), 0);
  assert.equal(p.toVisibleOffset(10), 1);
  assert.equal(p.toOriginalOffset(0), 0);
  assert.equal(p.toOriginalOffset(1), 10);
});
