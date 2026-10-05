import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SemanticTokenIndex } from '../src/semantic-token-index.ts';
import { TokenType, assertValidTokens } from '../src/analyzer.ts';
import type { SemanticToken } from '../src/analyzer.ts';
import type { TokenPatch } from '../src/diff.ts';

function apply(base: readonly SemanticToken[], patches: readonly TokenPatch[]): SemanticToken[] {
  return SemanticTokenIndex.applyPatches(base, patches);
}

test('first update patches the empty state into the full token list', () => {
  const index = new SemanticTokenIndex();
  assert.equal(index.version, -1);

  const delta = index.update('let x = 1;\nx;\n');
  assert.equal(index.version, 0);
  assert.equal(delta.fromVersion, -1);
  assert.equal(delta.toVersion, 0);
  assert.deepEqual(apply(index.tokens(-1), delta.patches), index.tokens(0));
});

test('renaming a declaration rebinds distant same-name references', () => {
  const index = new SemanticTokenIndex();
  index.update('let x = 1;\n{ let y = x; }\nx;\n');
  // Same-length rename keeps every offset stable; only bindings/types change.
  const delta = index.update('let z = 1;\n{ let y = x; }\nx;\n');

  // Two separated minimal intervals: the declaration site and the two
  // distant references. The untouched `y` declaration in between is kept.
  assert.equal(delta.patches.length, 2);

  const [declPatch, refPatch] = delta.patches;
  assert.deepEqual([declPatch.start, declPatch.end], [0, 1]);
  assert.equal(declPatch.tokens[0].type, TokenType.VariableDeclaration);
  assert.equal(declPatch.tokens[0].binding, 'z@4');

  assert.deepEqual([refPatch.start, refPatch.end], [2, 4]);
  assert.deepEqual(
    refPatch.tokens.map((t) => t.type),
    [TokenType.UnresolvedReference, TokenType.UnresolvedReference],
  );
  assert.deepEqual(
    refPatch.tokens.map((t) => t.binding),
    ['', ''],
  );

  // Incremental patch application reproduces the full recomputation.
  assert.deepEqual(apply(index.tokens(0), delta.patches), index.tokens(1));
});

test('adding a declaration binds a previously unresolved reference', () => {
  const index = new SemanticTokenIndex();
  index.update('x;\n');
  assert.equal(index.tokens(0)[0].type, TokenType.UnresolvedReference);

  const delta = index.update('let x = 1;\nx;\n');
  const tokens = index.tokens(1);
  assert.equal(tokens[0].type, TokenType.VariableDeclaration);
  assert.equal(tokens[1].type, TokenType.VariableReference);
  assert.equal(tokens[1].binding, tokens[0].binding);

  assert.deepEqual(apply(index.tokens(0), delta.patches), tokens);
});

test('adding a shadowing declaration rebinds inner references only', () => {
  const index = new SemanticTokenIndex();
  index.update('let x = 1;\n{\n  x;\n}\n');
  const before = index.tokens(0);
  assert.equal(before[1].type, TokenType.VariableReference);
  assert.equal(before[1].binding, before[0].binding);

  const delta = index.update('let x = 1;\n{\n  let x = 2;\n  x;\n}\n');
  const after = index.tokens(1);
  // The inner reference now binds to the new inner declaration.
  const innerDecl = after.find((t) => t.type === TokenType.VariableDeclaration && t !== after[0])!;
  const innerRef = after[after.length - 1];
  assert.equal(innerRef.binding, innerDecl.binding);
  assert.notEqual(innerRef.binding, after[0].binding);

  // The outer declaration token is untouched by the patch.
  assert.ok(!delta.patches.some((p) => p.start === 0 && p.end > 0));
  assert.deepEqual(apply(before, delta.patches), after);
});

test('removing a declaration unbinds its distant references', () => {
  const index = new SemanticTokenIndex();
  index.update('let x = 1;\nfn f() {\n  x;\n}\nx;\n');
  const delta = index.update('fn f() {\n  x;\n}\nx;\n');

  const tokens = index.tokens(1);
  const refs = tokens.filter((t) => t.type === TokenType.UnresolvedReference);
  assert.equal(refs.length, 2);
  assert.deepEqual(apply(index.tokens(0), delta.patches), tokens);
});

test('chained patch application equals the final full result', () => {
  const sources = [
    'let x = 1;\nx;\n',
    'let x = 1;\n{\n  let x = 2;\n  x;\n}\nx;\n',
    'let y = 1;\n{\n  let x = 2;\n  x;\n}\nx;\n',
    'let y = 1;\nfn x() {\n  y;\n}\nx();\ny;\n',
    'let y = 1;\nfn x() {\n  let y = 3;\n  y;\n}\nx();\ny;\n',
  ];

  const index = new SemanticTokenIndex();
  const deltas = sources.map((source) => index.update(source));
  assert.equal(index.version, sources.length - 1);

  // Each adjacent delta transforms its version into the next.
  for (let v = -1; v < index.version; v++) {
    const delta = index.delta(v);
    assert.equal(delta.fromVersion, v);
    assert.equal(delta.toVersion, v + 1);
    assert.deepEqual(apply(index.tokens(v), delta.patches), index.tokens(v + 1));
  }

  // Folding every patch from the empty state yields the final token list.
  let folded: readonly SemanticToken[] = index.tokens(-1);
  for (const delta of deltas) {
    folded = apply(folded, delta.patches);
  }
  assert.deepEqual(folded, index.tokens());
});

test('stored versions are immutable snapshots', () => {
  const index = new SemanticTokenIndex();
  index.update('let x = 1;\n');
  const v0 = index.tokens(0);
  const snapshot = JSON.parse(JSON.stringify(v0));
  index.update('let x = 1;\nx;\n');
  assert.deepEqual(v0, snapshot);
  assert.ok(Object.isFrozen(v0));
  assert.ok(Object.isFrozen(v0[0]));
});

test('delta() only serves adjacent versions', () => {
  const index = new SemanticTokenIndex();
  index.update('let x = 1;\n');
  index.update('let x = 2;\n');
  assert.throws(() => index.delta(1), RangeError);
  assert.throws(() => index.delta(-2), RangeError);
  assert.throws(() => index.delta(2), RangeError);
  assert.doesNotThrow(() => index.delta(0));
});

test('patches are sorted, non-overlapping intervals', () => {
  const index = new SemanticTokenIndex();
  index.update('let a = 1;\nlet b = a;\nlet c = b;\nc;\n');
  // Rename two declarations at once, forcing separated change regions.
  const delta = index.update('let a = 1;\nlet d = a;\nlet e = d;\ne;\n');
  for (let i = 0; i < delta.patches.length; i++) {
    const patch = delta.patches[i];
    assert.ok(patch.start <= patch.end);
    if (i > 0) assert.ok(patch.start >= delta.patches[i - 1].end);
  }
  assert.deepEqual(apply(index.tokens(0), delta.patches), index.tokens(1));
});

test('applyPatches validates its result', () => {
  const base: SemanticToken[] = [
    { start: 0, end: 1, type: TokenType.VariableDeclaration, binding: 'x@0' },
  ];
  const overlapping: TokenPatch[] = [
    {
      start: 0,
      end: 1,
      tokens: [
        { start: 0, end: 2, type: TokenType.VariableDeclaration, binding: 'x@0' },
        { start: 1, end: 3, type: TokenType.VariableReference, binding: 'x@0' },
      ],
    },
  ];
  assert.throws(() => SemanticTokenIndex.applyPatches(base, overlapping), /overlap/);
  assert.throws(
    () => SemanticTokenIndex.applyPatches(base, [{ start: 1, end: 3, tokens: [] }]),
    /invalid patch interval/,
  );
});

test('no-op update produces an empty patch list', () => {
  const index = new SemanticTokenIndex();
  index.update('let x = 1;\nx;\n');
  const delta = index.update('let x = 1;\nx;\n');
  assert.deepEqual(delta.patches, []);
  assert.deepEqual(apply(index.tokens(0), delta.patches), index.tokens(1));
});

test('tokens of every version satisfy the non-overlap invariant', () => {
  const index = new SemanticTokenIndex();
  const sources = [
    'fn f(x) {\n  let y = x;\n  y;\n}\nlet z = f(1);\nz;\n',
    'fn f(x) {\n  let x = 2;\n  x;\n}\nlet z = f(1);\nz;\n',
    'fn g(x) {\n  let x = 2;\n  x;\n}\nlet z = g(1);\nz;\n',
  ];
  for (const source of sources) {
    const delta = index.update(source);
    assertValidTokens(index.tokens());
    assertValidTokens(apply(index.tokens(delta.fromVersion), delta.patches));
  }
});
