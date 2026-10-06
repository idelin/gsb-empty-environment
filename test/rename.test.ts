import { test } from 'node:test';
import assert from 'node:assert/strict';
import { rename } from '../src/rename.ts';
import type { RenameResult } from '../src/rename.ts';
import { parse } from '../src/parser.ts';
import { bind } from '../src/binder.ts';
import { checkIsomorphic } from '../src/verify.ts';

const CURSOR = '▸';

function run(marked: string, newName: string): RenameResult {
  const offset = marked.indexOf(CURSOR);
  assert.notEqual(offset, -1, 'test source must contain a cursor marker');
  const source = marked.slice(0, offset) + marked.slice(offset + CURSOR.length);
  return rename(source, offset, newName);
}

function okSource(marked: string, newName: string): string {
  const result = run(marked, newName);
  assert.ok(result.ok, `expected rename to succeed, got: ${result.ok ? '' : result.reason}`);
  return result.ok ? result.newSource : '';
}

function rejected(marked: string, newName: string): string {
  const result = run(marked, newName);
  assert.ok(!result.ok, 'expected rename to be rejected');
  return result.ok ? '' : result.reason;
}

test('renames declaration and all bound references', () => {
  const out = okSource('let ▸x = 1; x + x;', 'total');
  assert.equal(out, 'let total = 1; total + total;');
});

test('rename can start from any reference of the binding', () => {
  const out = okSource('let x = 1; ▸x + x;', 'total');
  assert.equal(out, 'let total = 1; total + total;');
});

test('shadowed binding in inner scope is a different binding', () => {
  const out = okSource('let ▸x = 1; { let x = 2; x; } x;', 'outer');
  assert.equal(out, 'let outer = 1; { let x = 2; x; } outer;');
});

test('renaming the inner binding leaves outer references untouched', () => {
  const out = okSource('let x = 1; { let ▸x = 2; x; } x;', 'inner');
  assert.equal(out, 'let x = 1; { let inner = 2; inner; } x;');
});

test('same-named references in sibling scopes are untouched', () => {
  const out = okSource('{ let ▸x = 1; x; } { let x = 2; x; }', 'a');
  assert.equal(out, '{ let a = 1; a; } { let x = 2; x; }');
});

test('reference before declaration is not visible and resolves outward', () => {
  // The `x` inside the block precedes the inner `let x`, so it binds to the
  // outer declaration and must be renamed together with it.
  const out = okSource('let ▸x = 1; { x; let x = 2; x; }', 'outer');
  assert.equal(out, 'let outer = 1; { outer; let x = 2; x; }');
});

test('renaming the later declaration does not touch the pre-declaration reference', () => {
  const out = okSource('let x = 1; { x; let ▸x = 2; x; }', 'inner');
  assert.equal(out, 'let x = 1; { x; let inner = 2; inner; }');
});

test('unbound pre-declaration reference stays unbound, so rename is allowed', () => {
  // `y` precedes `let x` and cannot see it; after renaming x to y it still
  // resolves to nothing, so the rename is safe.
  const out = okSource('{ y; let ▸x = 1; x; }', 'y');
  assert.equal(out, '{ y; let y = 1; y; }');
});

test('initializer does not see its own declaration', () => {
  const out = okSource('let x = 1; { let ▸x = x + 1; x; }', 'y');
  assert.equal(out, 'let x = 1; { let y = x + 1; y; }');
});

test('rejects rename that would capture a renamed reference', () => {
  // After `let x = 2; let y = 3;`, renaming x to y would make the final `x`
  // resolve to the inner `let y` instead.
  const reason = rejected('let y = 1; { let ▸x = 2; let y = 3; x; }', 'y');
  assert.match(reason, /capture/);
});

test('rejects rename that would capture existing references to the new name', () => {
  // `y` in the block currently resolves to the outer `let y`; renaming the
  // inner `x` to `y` would steal it.
  const reason = rejected('let y = 1; { let ▸x = 2; y; }', 'y');
  assert.match(reason, /resolution/);
});

test('renaming to the outer name is fine when no reference resolution changes', () => {
  const out = okSource('let y = 1; { let ▸x = 2; x; } y;', 'y');
  assert.equal(out, 'let y = 1; { let y = 2; y; } y;');
});

test('same-block redeclaration: later declaration shadows earlier one', () => {
  const out = okSource('let x = 1; let ▸x = 2; x;', 'y');
  assert.equal(out, 'let x = 1; let y = 2; y;');
});

test('same-block redeclaration: earlier declaration has no references', () => {
  const out = okSource('let ▸x = 1; let x = 2; x;', 'y');
  assert.equal(out, 'let y = 1; let x = 2; x;');
});

test('rejects rename when a later same-block binding would capture references', () => {
  const reason = rejected('let ▸x = 1; let y = 2; x;', 'y');
  assert.match(reason, /capture/);
});

test('rejects renaming an unbound reference', () => {
  const reason = rejected('let x = 1; ▸y;', 'z');
  assert.match(reason, /unbound/);
});

test('rejects invalid new names', () => {
  assert.match(rejected('let ▸x = 1;', 'let'), /valid identifier/);
  assert.match(rejected('let ▸x = 1;', '1abc'), /valid identifier/);
  assert.match(rejected('let ▸x = 1;', 'a b'), /valid identifier/);
});

test('rejects rename with no identifier at the offset', () => {
  const result = rename('let x = 1;', 0, 'y');
  assert.ok(!result.ok);
});

test('renaming to the same name is a no-op', () => {
  const result = run('let ▸x = 1; x;', 'x');
  assert.ok(result.ok);
  if (result.ok) {
    assert.equal(result.newSource, 'let x = 1; x;');
    assert.equal(result.edits.length, 0);
  }
});

test('renames inside nested expressions and blocks', () => {
  const out = okSource('let ▸x = 1 + 2; { { let y = x * (x + 3); y; } }', 'v');
  assert.equal(out, 'let v = 1 + 2; { { let y = v * (v + 3); y; } }');
});

test('binding graph isomorphism check detects resolution changes', () => {
  const before = bind(parse('let y = 1; { let x = 2; y; }'));
  const after = bind(parse('let y = 1; { let y = 2; y; }'));
  const result = checkIsomorphic(before, after);
  assert.ok(!result.ok);
  if (!result.ok) {
    assert.equal(result.fromDecl, 0);
    assert.equal(result.toDecl, 1);
  }
});

test('binding graph isomorphism check accepts unchanged resolution', () => {
  const before = bind(parse('let x = 1; { let y = 2; x + y; }'));
  const after = bind(parse('let x = 1; { let z = 2; x + z; }'));
  assert.deepEqual(checkIsomorphic(before, after), { ok: true });
});
