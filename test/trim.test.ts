import { test } from 'node:test';
import assert from 'node:assert/strict';
import { trimSource, TrimError } from '../src/index.ts';
import type { TrimResult } from '../src/index.ts';

function trim(source: string, vars: Record<string, boolean> = {}): TrimResult {
  return trimSource(source, vars);
}

/** Every kept old line round-trips; every deleted old line maps to null. */
function assertMappingConsistent(result: TrimResult, oldLineCount: number): void {
  assert.equal(result.oldToNew.length, oldLineCount + 1);
  for (let oldLine = 1; oldLine <= oldLineCount; oldLine++) {
    const newLine = result.toNewLine(oldLine);
    assert.equal(newLine, result.oldToNew[oldLine]);
    if (newLine !== null) {
      assert.equal(result.toOldLine(newLine), oldLine);
    }
  }
  for (let newLine = 1; newLine < result.newToOld.length; newLine++) {
    assert.equal(result.newToOld[newLine], result.toOldLine(newLine));
    assert.ok(newLine === 1 || result.newToOld[newLine] > result.newToOld[newLine - 1]);
  }
}

test('basic if/else/endif trims and maps lines', () => {
  const src = ['line1', '#if DEBUG', 'debug-only', '#else', 'release', '#endif', 'line7', ''].join('\n');
  const on = trim(src, { DEBUG: true });
  assert.equal(on.text, 'line1\ndebug-only\nline7\n');
  assert.deepEqual(on.oldToNew, [null, 1, null, 2, null, null, null, 3]);
  assert.deepEqual(on.newToOld, [0, 1, 3, 7]);
  assert.equal(on.toNewLine(3), 2);
  assert.equal(on.toOldLine(3), 7);

  const off = trim(src, { DEBUG: false });
  assert.equal(off.text, 'line1\nrelease\nline7\n');
  assert.deepEqual(off.newToOld, [0, 1, 5, 7]);
  assertMappingConsistent(on, 7);
  assertMappingConsistent(off, 7);
});

test('nested conditions combine with parent state', () => {
  const src = ['a', '#if A', 'b', '#if B', 'c', '#endif', 'd', '#else', 'e', '#endif', 'f', ''].join('\n');
  assert.equal(trim(src, { A: true, B: false }).text, 'a\nb\nd\nf\n');
  assert.equal(trim(src, { A: true, B: true }).text, 'a\nb\nc\nd\nf\n');
  assert.equal(trim(src, { A: false, B: true }).text, 'a\ne\nf\n');
  const r = trim(src, { A: true, B: false });
  assert.deepEqual(r.newToOld, [0, 1, 3, 7, 11]);
  assertMappingConsistent(r, 11);
});

test('elif chain takes the first true branch and skips later evaluation', () => {
  const src = ['#if X', 'x', '#elif Y', 'y', '#elif Z', 'z', '#else', 'w', '#endif', ''].join('\n');
  assert.equal(trim(src, { X: true, Y: false, Z: false }).text, 'x\n');
  assert.equal(trim(src, { X: false, Y: true, Z: false }).text, 'y\n');
  assert.equal(trim(src, { X: false, Y: false, Z: true }).text, 'z\n');
  assert.equal(trim(src, { X: false, Y: false, Z: false }).text, 'w\n');
  // Z is unknown but never evaluated because Y already matched.
  assert.equal(trim(src, { X: false, Y: true }).text, 'y\n');
});

test('short-circuit evaluation avoids unknown variables', () => {
  assert.equal(trim('#if true || NOPE\na\n#endif\n').text, 'a\n');
  assert.equal(trim('#if false && NOPE\na\n#endif\n').text, '');
  assert.equal(trim('#if !true && NOPE\na\n#endif\n').text, '');
  assert.equal(trim('#if A || NOPE\na\n#endif\n', { A: true }).text, 'a\n');
  assert.throws(() => trim('#if false || NOPE\na\n#endif\n'), (e: unknown) => {
    assert.ok(e instanceof TrimError);
    assert.equal((e as TrimError).code, 'UNKNOWN_VARIABLE');
    return true;
  });
  assert.throws(() => trim('#if true && NOPE\na\n#endif\n'), /unknown variable 'NOPE'/);
});

test('unknown variable error carries the variable name and line', () => {
  try {
    trim('ok\n#if NOPE && true\na\n#endif\n');
    assert.fail('should have thrown');
  } catch (e) {
    assert.ok(e instanceof TrimError);
    assert.equal(e.code, 'UNKNOWN_VARIABLE');
    assert.equal(e.line, 2);
    assert.match(e.message, /NOPE/);
  }
});

test('pseudo-directives inside template literals are kept as text', () => {
  const src = [
    'const tpl = `',
    '#if DEBUG',
    'should-stay',
    '#endif',
    '`;',
    'const s = "#endif";',
    "#if DEBUG",
    'gone',
    '#endif',
    'after',
    '',
  ].join('\n');
  const r = trim(src, { DEBUG: false });
  assert.equal(
    r.text,
    ['const tpl = `', '#if DEBUG', 'should-stay', '#endif', '`;', 'const s = "#endif";', 'after', ''].join('\n'),
  );
  assert.equal(r.toNewLine(8), null); // 'gone' removed
  assert.equal(r.toNewLine(2), 2); // template line kept verbatim
  assertMappingConsistent(r, 10);
});

test('escaped quotes and template substitutions do not confuse the lexer', () => {
  const src = [
    'const a = "x\\" #if NOPE";',
    'const b = `${1 + 2}`;',
    'const c = `nested ${`inner #endif`} done`;',
    '#if A',
    'yes',
    '#endif',
    '',
  ].join('\n');
  const r = trim(src, { A: true });
  // Strings are kept verbatim; the real directives around 'yes' are removed.
  assert.equal(r.text, [
    'const a = "x\\" #if NOPE";',
    'const b = `${1 + 2}`;',
    'const c = `nested ${`inner #endif`} done`;',
    'yes',
    '',
  ].join('\n'));
});

test('directives inside block comments spanning lines are ignored', () => {
  const src = [
    'a',
    '/* comment start',
    '#if DEBUG',
    'x',
    '#endif',
    'comment end */',
    '#if B',
    'kept',
    '#endif',
    'b',
    '',
  ].join('\n');
  const r = trim(src, { B: true, DEBUG: false });
  // The comment body (including fake directives) survives untouched;
  // only the real #if/#endif lines are removed.
  assert.equal(
    r.text,
    ['a', '/* comment start', '#if DEBUG', 'x', '#endif', 'comment end */', 'kept', 'b', ''].join('\n'),
  );
  assertMappingConsistent(r, 10);
});

test('line comments and same-line block comments hide directives', () => {
  const src = ['// #if DEBUG', '/* #if DEBUG */', 'x /*', '#endif', '*/ y', ''].join('\n');
  assert.equal(trim(src).text, src);
});

test('unclosed condition reports the opening line', () => {
  assert.throws(() => trim('#if A\nfoo\n', { A: true }), (e: unknown) => {
    assert.ok(e instanceof TrimError);
    assert.equal((e as TrimError).code, 'UNCLOSED_CONDITION');
    assert.equal((e as TrimError).line, 1);
    return true;
  });
  // Inner #endif closes the inner #if; the outer one (line 1) stays open.
  assert.throws(() => trim('#if A\n#if B\nx\n#endif\n', { A: true, B: true }), (e: unknown) => {
    assert.equal((e as TrimError).code, 'UNCLOSED_CONDITION');
    assert.equal((e as TrimError).line, 1);
    return true;
  });
});

test('stray directives are rejected', () => {
  for (const bad of ['#endif\n', '#else\n', '#elif A\n']) {
    assert.throws(() => trim(bad), (e: unknown) => {
      assert.ok(e instanceof TrimError);
      assert.equal((e as TrimError).code, 'UNEXPECTED_DIRECTIVE');
      return true;
    });
  }
});

test('duplicate else and elif after else are rejected', () => {
  assert.throws(() => trim('#if A\n#else\n#else\n#endif\n', { A: true }), (e: unknown) => {
    assert.equal((e as TrimError).code, 'DUPLICATE_ELSE');
    return true;
  });
  assert.throws(() => trim('#if A\n#else\n#elif B\n#endif\n', { A: true, B: true }), (e: unknown) => {
    assert.equal((e as TrimError).code, 'ELIF_AFTER_ELSE');
    return true;
  });
});

test('empty branches produce no lines and no errors', () => {
  assert.equal(trim('#if X\n#endif\na\n', { X: true }).text, 'a\n');
  assert.equal(trim('#if X\n#else\nb\n#endif\n', { X: true }).text, '');
  assert.equal(trim('#if X\n#else\nb\n#endif\n', { X: false }).text, 'b\n');
  assert.equal(trim('#if X\n#elif Y\n#else\n#endif\n', { X: false, Y: false }).text, '');
});

test('dead branches do not evaluate nested conditions', () => {
  const src = '#if OFF\n#if NOPE\na\n#else\nb\n#endif\n#endif\ndone\n';
  assert.equal(trim(src, { OFF: false }).text, 'done\n');
});

test('deleted ranges map to null in the forward direction', () => {
  const src = ['keep1', '#if OFF', 'dead1', 'dead2', '#else', 'alive', '#endif', 'keep2', ''].join('\n');
  const r = trim(src, { OFF: false });
  assert.equal(r.text, 'keep1\nalive\nkeep2\n');
  // Contiguous deleted range (directive + dead lines) all map to null.
  assert.deepEqual([2, 3, 4, 5, 7].map((l) => r.toNewLine(l)), [null, null, null, null, null]);
  assert.equal(r.toNewLine(6), 2);
  // Reverse queries never point into the deleted range.
  assert.deepEqual(r.newToOld, [0, 1, 6, 8]);
  assertMappingConsistent(r, 8);
});

test('out-of-range mapping queries throw RangeError', () => {
  const r = trim('a\n#if X\nb\n#endif\n', { X: true });
  assert.throws(() => r.toNewLine(0), RangeError);
  assert.throws(() => r.toNewLine(5), RangeError);
  assert.throws(() => r.toOldLine(0), RangeError);
  assert.throws(() => r.toOldLine(3), RangeError);
});

test('crlf line endings are preserved', () => {
  const r = trim('a\r\n#if X\r\nb\r\n#endif\r\nc\r\n', { X: false });
  assert.equal(r.text, 'a\r\nc\r\n');
  assert.deepEqual(r.newToOld, [0, 1, 5]);
});

test('expression syntax errors are reported', () => {
  for (const bad of ['#if (A\n', '#if A B\n', '#if A &&\n', '#if\n', '#elif\n']) {
    const src = bad.startsWith('#elif') ? `#if A\n${bad}#endif\n` : `${bad}#endif\n`;
    assert.throws(() => trim(src, { A: true }), (e: unknown) => {
      assert.ok(e instanceof TrimError);
      assert.equal((e as TrimError).code, 'INVALID_EXPRESSION');
      return true;
    });
  }
  assert.throws(() => trim('#if A\n#else junk\n#endif\n', { A: true }), /unexpected content after '#else'/);
  assert.throws(() => trim('#if A\n#endif x\n', { A: true }), /unexpected content after '#endif'/);
});

test('trailing comments on directive lines are allowed', () => {
  const src = '#if A // toggle\nx\n#endif /* done */\n';
  assert.equal(trim(src, { A: true }).text, 'x\n');
});

test('boolean literals, negation and parentheses work', () => {
  assert.equal(trim('#if !false\na\n#endif\n').text, 'a\n');
  assert.equal(trim('#if (A && !B) || (B && !A)\nx\n#endif\n', { A: true, B: false }).text, 'x\n');
  assert.equal(trim('#if (A && !B) || (B && !A)\nx\n#endif\n', { A: true, B: true }).text, '');
});

test('unknown #xxx lines are ordinary code, not directives', () => {
  const src = '#include <stdio.h>\n#define X 1\n';
  assert.equal(trim(src).text, src);
});

test('no directives yields identity mapping', () => {
  const r = trim('a\nb\nc\n');
  assert.equal(r.text, 'a\nb\nc\n');
  assert.deepEqual(r.oldToNew, [null, 1, 2, 3]);
  assert.deepEqual(r.newToOld, [0, 1, 2, 3]);
  assertMappingConsistent(r, 3);
});

test('empty source yields empty output', () => {
  const r = trim('');
  assert.equal(r.text, '');
  assert.deepEqual(r.oldToNew, [null]);
  assert.deepEqual(r.newToOld, [0]);
});
