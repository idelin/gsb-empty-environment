import { SemanticTokenIndex } from '../src/semanticIndex';
import { applyPatches, diffTokens, TokenPatch } from '../src/diff';
import { assertWellFormed, SemanticToken } from '../src/analyzer';
import { test, assert, assertDeepEqual } from './harness';

function assertPatchConsistency(
  oldTokens: SemanticToken[],
  newTokens: SemanticToken[],
  patches: TokenPatch[],
  label: string,
): void {
  const patched = applyPatches(oldTokens, patches);
  assertDeepEqual(patched, newTokens, `${label}: incremental patch application must equal full result`);
  assertWellFormed(patched);
}

test('update returns patches; applying them reproduces the full token list', () => {
  const index = new SemanticTokenIndex('let x = 1;\nx;');
  const v0 = index.getTokens(0);
  const patches = index.update('let x = 1;\nlet y = 2;\nx + y;');
  assert(index.version === 1, 'version increments');
  assertPatchConsistency(v0, index.getTokens(1), patches, 'append declaration');
});

test('new declaration resolves previously unresolved references', () => {
  const index = new SemanticTokenIndex('foo;\nfoo;');
  const before = index.getTokens(0);
  assert(before.every((t) => t.type === 'unresolved'), 'unresolved before');

  const patches = index.update('let foo;\nfoo;\nfoo;');
  const after = index.getTokens(1);
  assert(after[0].type === 'declaration', 'new declaration token');
  assert(after[1].decl === after[0].decl && after[2].decl === after[0].decl, 'references rebound');

  assertPatchConsistency(before, after, patches, 'new declaration');
  assert(patches.length > 0, 'non-empty patch list');
});

test('declaration rename patches distant same-name references', () => {
  const padding = '// 😀😀😀 far away\n'.repeat(3);
  const index = new SemanticTokenIndex(`let a = 1;\nlet keep = 2;\n${padding}a + a;`);
  const before = index.getTokens(0);
  const patches = index.update(`let b = 1;\nlet keep = 2;\n${padding}a + a;`);
  const after = index.getTokens(1);

  assert(after[0].decl === 'b@4', 'declaration renamed');
  assert(after[2].type === 'unresolved' && after[3].type === 'unresolved', 'references unbound');
  assertPatchConsistency(before, after, patches, 'rename with distant references');

  // The untouched `keep` declaration sits between the rename and the distant
  // references, so the minimal script needs exactly two replacement intervals.
  assert(patches.length === 2, `expected 2 minimal intervals, got ${patches.length}`);
});

test('single-token change yields a single minimal replacement interval', () => {
  const index = new SemanticTokenIndex('let a;\nlet b;\na;');
  const before = index.getTokens(0);
  const patches = index.update('let a;\nlet b;\nb;');
  assert(patches.length === 1, 'exactly one patch');
  assert(patches[0].deleteCount === 1 && patches[0].tokens.length === 1, '1:1 replacement');
  assert(patches[0].tokens[0].decl === index.getTokens(1)[1].decl, 'rebound to b');
  assertPatchConsistency(before, index.getTokens(1), patches, 'single token change');
});

test('identical source produces an empty patch list', () => {
  const index = new SemanticTokenIndex('let x = 1;\nx;');
  const patches = index.update('let x = 1;\nx;');
  assert(patches.length === 0, 'no patches for identical source');
  assert(index.version === 1, 'version still increments');
});

test('multi-version chain: every adjacent diff patches to the full result', () => {
  const sources = [
    'let x = 1;\nx;',
    'let x = 1;\n{\n  let x = 2;\n  x;\n}\nx;',
    'let x = 1;\n{\n  let y = 2;\n  x;\n}\nx;',
    'let 变量 = 1;\n{\n  let y = 变量;\n  变量;\n}\n变量;',
    'let 变量 = 1;\n变量;',
  ];
  const index = new SemanticTokenIndex(sources[0]);
  for (let i = 1; i < sources.length; i++) {
    const patches = index.update(sources[i]);
    assertPatchConsistency(
      index.getTokens(i - 1),
      index.getTokens(i),
      patches,
      `version ${i - 1} -> ${i}`,
    );
    // diff(from, to) must agree with what update returned.
    assertDeepEqual(index.diff(i - 1, i), patches, `diff(${i - 1}, ${i}) matches update result`);
  }
});

test('diffTokens handles empty <-> non-empty boundaries', () => {
  const index = new SemanticTokenIndex('');
  const v0 = index.getTokens(0);
  assert(v0.length === 0, 'empty source has no tokens');
  const patches = index.update('let x = 1;');
  assert(patches.length === 1 && patches[0].start === 0 && patches[0].deleteCount === 0, 'pure insertion');
  assertPatchConsistency(v0, index.getTokens(1), patches, 'empty -> non-empty');

  const patchesBack = index.update('');
  assert(patchesBack.length === 1 && patchesBack[0].tokens.length === 0, 'pure deletion');
  assertPatchConsistency(index.getTokens(1), index.getTokens(2), patchesBack, 'non-empty -> empty');
});

test('shadowing change deep in nested scopes stays consistent', () => {
  const before = 'let a;\n{\n  {\n    let a;\n    a;\n  }\n  a;\n}\na;';
  const after = 'let a;\n{\n  {\n    let b;\n    a;\n  }\n  a;\n}\na;';
  const index = new SemanticTokenIndex(before);
  const oldTokens = index.getTokens(0);
  const patches = index.update(after);
  const newTokens = index.getTokens(1);

  // Removing the inner declaration rebinds the deeply nested `a` reference
  // to the outermost declaration.
  const outerKey = 'a@4';
  const deepRef = newTokens.find((t) => t.start === after.indexOf('a;', after.indexOf('let b')));
  assert(deepRef !== undefined && deepRef.decl === outerKey, 'deep reference rebound to outer decl');
  assertPatchConsistency(oldTokens, newTokens, patches, 'nested shadowing change');
});
