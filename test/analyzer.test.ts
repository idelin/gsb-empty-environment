import { analyze, assertWellFormed, SemanticToken } from '../src/analyzer';
import { SourceText } from '../src/sourceText';
import { test, assert, assertDeepEqual } from './harness';

function tokensOf(src: string): SemanticToken[] {
  const tokens = analyze(new SourceText(src));
  assertWellFormed(tokens);
  return tokens;
}

test('declaration and reference resolve within one scope', () => {
  const src = 'let x = 1;\nx + x;';
  const tokens = tokensOf(src);
  assertDeepEqual(
    tokens.map((t) => t.type),
    ['declaration', 'reference', 'reference'],
    'token types',
  );
  const declKey = `x@${src.indexOf('x')}`;
  assert(tokens[0].decl === declKey, 'declaration carries its own key');
  assert(tokens[1].decl === declKey && tokens[2].decl === declKey, 'references bind to decl');
  assert(tokens[0].start === 4 && tokens[0].length === 1, 'declaration position');
});

test('shadowing: inner declaration wins, outer references unaffected', () => {
  const src = 'let a = 1;\n{\n  let a = 2;\n  a;\n}\na;';
  const tokens = tokensOf(src);
  assertDeepEqual(
    tokens.map((t) => t.type),
    ['declaration', 'declaration', 'reference', 'reference'],
    'token types',
  );
  const outerKey = `a@${src.indexOf('a')}`;
  const innerKey = `a@${src.indexOf('a', src.indexOf('let a = 2'))}`;
  assert(tokens[2].decl === innerKey, 'inner reference resolves to inner declaration');
  assert(tokens[3].decl === outerKey, 'outer reference resolves to outer declaration');
});

test('renaming an inner declaration rebinds distant same-name references', () => {
  // Before: inner `a` binds to inner decl. After renaming inner decl to `b`,
  // the inner `a` reference falls through to the outer declaration.
  const before = tokensOf('let a = 1;\n{\n  let a = 2;\n  a;\n}');
  const after = tokensOf('let a = 1;\n{\n  let b = 2;\n  a;\n}');
  const outerKey = `a@4`;
  assert(before[2].decl !== outerKey, 'inner ref bound to inner decl before rename');
  assert(after[2].decl === outerKey, 'inner ref rebinds to outer decl after rename');
});

test('unicode identifiers and astral characters keep UTF-16 positions', () => {
  const src = '// 😀 comment\nlet 变量 = "😀😀";\n变量 + 变量;';
  const tokens = tokensOf(src);
  assertDeepEqual(
    tokens.map((t) => t.type),
    ['declaration', 'reference', 'reference'],
    'token types',
  );
  const declStart = src.indexOf('变量');
  assert(tokens[0].start === declStart && tokens[0].length === 2, 'declaration utf16 position');
  assert(tokens[1].start === src.indexOf('变量', declStart + 1), 'reference position after astral string');
  assert(tokens[1].decl === tokens[0].decl && tokens[2].decl === tokens[0].decl, 'binding keys');
});

test('unresolved and property tokens', () => {
  const tokens = tokensOf('obj.field;\nghost;');
  assertDeepEqual(
    tokens.map((t) => t.type),
    ['unresolved', 'property', 'unresolved'],
    'token types',
  );
});

test('tokens never overlap and are sorted', () => {
  const src = 'let x = 1;\n{ let y = x; y; }\nlet z = x;\nz;';
  const tokens = tokensOf(src);
  for (let i = 1; i < tokens.length; i++) {
    assert(
      tokens[i].start >= tokens[i - 1].start + tokens[i - 1].length,
      `overlap at token ${i}`,
    );
  }
});
