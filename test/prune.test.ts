import { test } from 'node:test';
import assert from 'node:assert/strict';
import { pruneSource } from '../src/prune.ts';
import { DirectiveError } from '../src/errors.ts';

test('basic pruning keeps active branch and maps lines', () => {
  const source = [
    'line1',      // 1
    '// #if DEBUG',
    'debug',      // 3
    '// #else',
    'release',    // 5
    '// #endif',
    'line7',      // 7
    '',
  ].join('\n');
  const result = pruneSource(source, { vars: { DEBUG: true } });
  assert.equal(result.text, 'line1\ndebug\nline7\n');
  const { mapping } = result;
  assert.equal(mapping.toOriginalLine(1), 1);
  assert.equal(mapping.toOriginalLine(2), 3);
  assert.equal(mapping.toOriginalLine(3), 7);
  assert.equal(mapping.toGeneratedLine(3), 2);
  assert.equal(mapping.toGeneratedLine(7), 3);
  // Deleted lines map to explicit null.
  assert.equal(mapping.toGeneratedLine(5), null);
  assert.ok(mapping.isDeleted(5));
  assert.ok(!mapping.isDeleted(7));
});

test('else branch is kept when condition is false', () => {
  const source = '// #if DEBUG\ndebug\n// #else\nrelease\n// #endif\n';
  const result = pruneSource(source, { vars: { DEBUG: false } });
  assert.equal(result.text, 'release\n');
  assert.equal(result.mapping.toOriginalLine(1), 4);
});

test('nested conditions', () => {
  const source = [
    'a',          // 1
    '// #if A',   // 2
    'b',          // 3
    '// #if B',   // 4
    'c',          // 5
    '// #endif',  // 6
    'd',          // 7
    '// #endif',  // 8
    'e',          // 9
    '',
  ].join('\n');
  const both = pruneSource(source, { vars: { A: true, B: true } });
  assert.equal(both.text, 'a\nb\nc\nd\ne\n');
  const inner = pruneSource(source, { vars: { A: true, B: false } });
  assert.equal(inner.text, 'a\nb\nd\ne\n');
  assert.equal(inner.mapping.toOriginalLine(3), 7);
  // Outer false: inner #if is never evaluated, so B may stay unknown.
  const outer = pruneSource(source, { vars: { A: false } });
  assert.equal(outer.text, 'a\ne\n');
});

test('elif chain', () => {
  const source = [
    '// #if A',
    'a',
    '// #elif B',
    'b',
    '// #elif C',
    'c',
    '// #else',
    'd',
    '// #endif',
    '',
  ].join('\n');
  const vars = { A: false, B: false, C: true };
  assert.equal(pruneSource(source, { vars }).text, 'c\n');
  assert.equal(pruneSource(source, { vars: { A: false, B: false, C: false } }).text, 'd\n');
  assert.equal(pruneSource(source, { vars: { A: true, B: true, C: true } }).text, 'a\n');
});

test('short-circuit conditions with unknown variables', () => {
  const source = '// #if false && UNKNOWN\nx\n// #endif\n// #if true || UNKNOWN\ny\n// #endif\n';
  assert.equal(pruneSource(source).text, 'y\n');
  assert.throws(
    () => pruneSource('// #if true && UNKNOWN\nx\n// #endif\n'),
    (err: unknown) => {
      assert.ok(err instanceof DirectiveError);
      assert.match((err as Error).message, /unknown variable 'UNKNOWN'/);
      assert.equal((err as DirectiveError).line, 1);
      return true;
    },
  );
});

test('unknown variable in an active condition errors', () => {
  assert.throws(() => pruneSource('// #if NOPE\nx\n// #endif\n'), DirectiveError);
});

test('unknown variable in a skipped #elif is not evaluated', () => {
  const source = '// #if true\na\n// #elif UNKNOWN\nb\n// #endif\n';
  assert.equal(pruneSource(source).text, 'a\n');
});

test('pseudo-directives inside strings are ignored', () => {
  const source = [
    'const a = "// #if X";',
    "const b = '// #endif';",
    'const c = "q\\" // #if ESCAPED";',
    'const t = `template',
    '// #if X',
    'end`;',
    '// #if KEEP',
    'yes',
    '// #endif',
    '',
  ].join('\n');
  const result = pruneSource(source, { vars: { KEEP: true } });
  assert.equal(result.text, source.replace('// #if KEEP\n', '').replace('// #endif\n', ''));
});

test('pseudo-directives inside template interpolation are ignored', () => {
  const source = 'const t = `${x // #if not-a-directive\n}`;\nkept\n';
  const result = pruneSource(source);
  assert.equal(result.text, source);
});

test('pseudo-directives inside block comments spanning lines are ignored', () => {
  const source = [
    '/*',
    '// #if X',
    '// #endif',
    '*/',
    'kept',
    '',
  ].join('\n');
  const result = pruneSource(source);
  assert.equal(result.text, source);
  assert.equal(result.mapping.generatedLineCount, 5);
});

test('block comment state survives across lines around real directives', () => {
  const source = [
    '/* start',
    'still comment // #endif',
    'end */',
    '// #if A',
    'yes',
    '// #endif',
    '',
  ].join('\n');
  const result = pruneSource(source, { vars: { A: true } });
  assert.equal(result.text, '/* start\nstill comment // #endif\nend */\nyes\n');
});

test('unclosed conditional errors with the #if line', () => {
  assert.throws(
    () => pruneSource('a\n// #if X\nb\n', { vars: { X: true } }),
    (err: unknown) => {
      assert.ok(err instanceof DirectiveError);
      assert.match((err as Error).message, /unclosed conditional/);
      assert.equal((err as DirectiveError).line, 2);
      return true;
    },
  );
});

test('unmatched directives error', () => {
  assert.throws(() => pruneSource('// #endif\n'), /#endif without matching #if/);
  assert.throws(() => pruneSource('// #else\n'), /#else without matching #if/);
  assert.throws(() => pruneSource('// #elif A\n', { vars: { A: true } }), /#elif without matching #if/);
  assert.throws(() => pruneSource('// #if A\n// #else\n// #else\n// #endif\n', { vars: { A: true } }), /duplicate #else/);
  assert.throws(() => pruneSource('// #if A\n// #else\n// #elif B\n// #endif\n', { vars: { A: true, B: true } }), /#elif after #else/);
  assert.throws(() => pruneSource('// #if\n// #endif\n'), /missing condition/);
  assert.throws(() => pruneSource('// #if A\n// #else B\n// #endif\n', { vars: { A: true } }), /#else does not take/);
});

test('empty branches prune cleanly', () => {
  const source = [
    'a',          // 1
    '// #if X',   // 2
    '// #endif',  // 3
    'b',          // 4
    '// #if Y',   // 5
    '// #else',   // 6
    '// #endif',  // 7
    'c',          // 8
    '',
  ].join('\n');
  const result = pruneSource(source, { vars: { X: true, Y: false } });
  assert.equal(result.text, 'a\nb\nc\n');
  assert.equal(result.mapping.generatedLineCount, 3);
  assert.deepEqual(result.mapping.deletedRanges(), [
    { startLine: 2, endLine: 3 },
    { startLine: 5, endLine: 7 },
  ]);
});

test('bidirectional position mapping round-trips', () => {
  const source = [
    'one',    // 1
    '// #if OFF',
    'two',    // 3
    'three',  // 4
    '// #endif',
    'four',   // 6
    '',
  ].join('\n');
  const { mapping } = pruneSource(source, { vars: { OFF: false } });

  // Forward: kept lines map, deleted lines are explicitly null.
  assert.equal(mapping.toGeneratedLine(1), 1);
  assert.equal(mapping.toGeneratedLine(6), 2);
  assert.equal(mapping.toGeneratedLine(3), null);
  assert.equal(mapping.toGeneratedLine(4), null);

  // Reverse: generated lines map back to originals.
  assert.equal(mapping.toOriginalLine(1), 1);
  assert.equal(mapping.toOriginalLine(2), 6);
  assert.equal(mapping.toOriginalLine(3), null);
  assert.equal(mapping.toOriginalLine(0), null);

  // Positions preserve columns; deleted positions are explicitly null.
  assert.deepEqual(mapping.toGeneratedPosition({ line: 6, column: 2 }), { line: 2, column: 2 });
  assert.equal(mapping.toGeneratedPosition({ line: 3, column: 0 }), null);
  assert.deepEqual(mapping.toOriginalPosition({ line: 2, column: 1 }), { line: 6, column: 1 });

  // Round trip for every surviving line.
  for (const original of [1, 6]) {
    const generated = mapping.toGeneratedLine(original);
    assert.ok(generated !== null);
    assert.equal(mapping.toOriginalLine(generated), original);
  }
});

test('range queries: deleted ranges are explicitly empty', () => {
  const source = [
    'one',    // 1
    '// #if OFF',
    'two',    // 3
    'three',  // 4
    '// #endif',
    'four',   // 6
    '',
  ].join('\n');
  const { mapping } = pruneSource(source, { vars: { OFF: false } });
  // A fully deleted original range maps to an empty list of generated ranges.
  assert.deepEqual(mapping.toGeneratedRange(2, 5), []);
  // A partially surviving range maps to the surviving generated lines.
  assert.deepEqual(mapping.toGeneratedRange(1, 7), [
    { startLine: 1, endLine: 2 },
  ]);
  assert.deepEqual(mapping.deletedRanges(), [{ startLine: 2, endLine: 5 }]);
});

test('comparison conditions in directives', () => {
  const source = '// #if MODE == "prod"\nprod\n// #elif LEVEL != 1\nhigh\n// #else\nlow\n// #endif\n';
  assert.equal(pruneSource(source, { vars: { MODE: 'prod', LEVEL: 1 } }).text, 'prod\n');
  assert.equal(pruneSource(source, { vars: { MODE: 'dev', LEVEL: 2 } }).text, 'high\n');
  assert.equal(pruneSource(source, { vars: { MODE: 'dev', LEVEL: 1 } }).text, 'low\n');
});

test('no trailing newline and empty source', () => {
  assert.equal(pruneSource('a\n// #if X\nb\n// #endif', { vars: { X: false } }).text, 'a');
  assert.equal(pruneSource('').text, '');
});
