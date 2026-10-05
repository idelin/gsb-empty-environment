import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ScalarText } from '../src/scalar-text.ts';
import { TokenType, analyzeSource, assertValidTokens } from '../src/analyzer.ts';
import type { SemanticToken } from '../src/analyzer.ts';

function analyze(source: string): SemanticToken[] {
  const tokens = analyzeSource(ScalarText.from(source));
  assertValidTokens(tokens);
  return tokens;
}

function types(tokens: readonly SemanticToken[]): string[] {
  return tokens.map((t) => t.type);
}

test('declarations and references bind within one scope', () => {
  const tokens = analyze('let x = 1;\nx;\n');
  assert.deepEqual(types(tokens), [
    TokenType.VariableDeclaration,
    TokenType.VariableReference,
  ]);
  assert.equal(tokens[1].binding, tokens[0].binding);
  assert.equal(tokens[0].binding, `x@${'let '.length}`);
});

test('inner block declaration shadows the outer one', () => {
  const source = 'let x = 1;\n{\n  let x = 2;\n  x;\n}\nx;\n';
  const tokens = analyze(source);
  assert.deepEqual(types(tokens), [
    TokenType.VariableDeclaration, // outer x
    TokenType.VariableDeclaration, // inner x
    TokenType.VariableReference, // inner reference
    TokenType.VariableReference, // outer reference
  ]);
  const [outerDecl, innerDecl, innerRef, outerRef] = tokens;
  assert.notEqual(innerDecl.binding, outerDecl.binding);
  assert.equal(innerRef.binding, innerDecl.binding);
  assert.equal(outerRef.binding, outerDecl.binding);
});

test('a let initializer resolves in the enclosing scope', () => {
  const tokens = analyze('let x = 1;\nlet x = x;\n');
  const [outerDecl, innerDecl, initRef] = tokens;
  assert.equal(initRef.type, TokenType.VariableReference);
  assert.equal(initRef.binding, outerDecl.binding);
  assert.notEqual(innerDecl.binding, outerDecl.binding);
});

test('unresolved references are reported without a binding', () => {
  const tokens = analyze('y;\n');
  assert.deepEqual(types(tokens), [TokenType.UnresolvedReference]);
  assert.equal(tokens[0].binding, '');
});

test('function parameters shadow outer bindings inside the body', () => {
  const source = 'let x = 1;\nfn f(x) {\n  x;\n}\nx;\n';
  const tokens = analyze(source);
  assert.deepEqual(types(tokens), [
    TokenType.VariableDeclaration, // outer x
    TokenType.FunctionDeclaration, // f
    TokenType.ParameterDeclaration, // param x
    TokenType.ParameterReference, // x in body -> param
    TokenType.VariableReference, // x after fn -> outer
  ]);
  const [outerDecl, fnDecl, paramDecl, bodyRef, outerRef] = tokens;
  assert.equal(bodyRef.binding, paramDecl.binding);
  assert.equal(outerRef.binding, outerDecl.binding);
  assert.equal(fnDecl.binding, 'f@14');
});

test('function names are visible in the enclosing scope', () => {
  const tokens = analyze('fn g() {\n}\ng();\n');
  assert.deepEqual(types(tokens), [
    TokenType.FunctionDeclaration,
    TokenType.FunctionReference,
  ]);
  assert.equal(tokens[1].binding, tokens[0].binding);
});

test('later same-scope declaration shadows the earlier one', () => {
  const tokens = analyze('let x = 1;\nlet x = 2;\nx;\n');
  const [first, second, ref] = tokens;
  assert.notEqual(first.binding, second.binding);
  assert.equal(ref.binding, second.binding);
});

test('token positions are UTF-16 offsets across astral characters', () => {
  // The comment contains an emoji (1 scalar, 2 UTF-16 units).
  const source = '// \u{1F600}\nlet x = 1;\nx;\n';
  const text = ScalarText.from(source);
  const tokens = analyze(source);

  const declScalar = 9; // '/', '/', ' ', emoji, '\n', 'l', 'e', 't', ' '
  assert.equal(text.scalarOffsetToUtf16(declScalar), 10);
  assert.equal(tokens[0].start, 10);
  assert.equal(tokens[0].end, 11);
  // The reference after the declaration: scalar 16 -> utf16 17.
  assert.equal(tokens[1].start, text.scalarOffsetToUtf16(16));
  assert.equal(tokens[1].start, 17);
});

test('unicode identifiers are supported', () => {
  const tokens = analyze('let \u53D8\u91CF = 1;\n\u53D8\u91CF;\n');
  assert.deepEqual(types(tokens), [
    TokenType.VariableDeclaration,
    TokenType.VariableReference,
  ]);
  assert.equal(tokens[1].binding, tokens[0].binding);
});

test('tokens never overlap and are sorted by construction', () => {
  const source = [
    'fn add(a, b) {',
    '  let sum = a + b;',
    '  sum;',
    '}',
    'let total = add(1, 2);',
    '{ let total = 0; total; }',
    'total;',
    '',
  ].join('\n');
  const tokens = analyze(source);
  for (let i = 1; i < tokens.length; i++) {
    assert.ok(tokens[i].start >= tokens[i - 1].end, `token ${i} overlaps previous`);
  }
});

test('assertValidTokens rejects overlapping tokens', () => {
  const bad: SemanticToken[] = [
    { start: 0, end: 3, type: TokenType.VariableDeclaration, binding: 'a@0' },
    { start: 2, end: 4, type: TokenType.VariableReference, binding: 'a@0' },
  ];
  assert.throws(() => assertValidTokens(bad), /overlap/);
  assert.throws(
    () => assertValidTokens([{ start: 2, end: 2, type: TokenType.VariableReference, binding: '' }]),
    /invalid token range/,
  );
});

test('syntax errors carry scalar offsets', () => {
  assert.throws(() => analyze('let = 1;\n'), /scalar offset 4/);
  assert.throws(() => analyze('let x = ;\n'), /scalar offset 8/);
});
