import { SemanticTokenIndex } from '../src/semanticIndex';
import { applyPatches } from '../src/diff';
import { assertWellFormed } from '../src/analyzer';
import { test, assertDeepEqual } from './harness';

// Deterministic PRNG (mulberry32) so failures are reproducible.
function rng(seed: number): () => number {
  let state = seed;
  return () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const FRAGMENTS = [
  'let x = 1;\n',
  'let 变量 = "😀";\n',
  'x + 变量;\n',
  '{\n  let y = x;\n  y;\n}\n',
  'obj.field;\n',
  '// 😀 comment\n',
  'ghost;\n',
  'const z = 2;\n',
];

test('fuzz: random edits, incremental patches always match full analysis', () => {
  const rand = rng(20261005);
  const pick = () => FRAGMENTS[Math.floor(rand() * FRAGMENTS.length)];

  let lines: string[] = [pick(), pick(), pick()];
  const index = new SemanticTokenIndex(lines.join(''));

  for (let step = 0; step < 200; step++) {
    const op = rand();
    const at = Math.floor(rand() * (lines.length + 1));
    if (op < 0.4) {
      lines.splice(at, 0, pick()); // insert
    } else if (op < 0.8 && lines.length > 0) {
      lines.splice(Math.floor(rand() * lines.length), 1); // delete
    } else {
      lines.splice(Math.floor(rand() * lines.length), 1, pick()); // replace
    }
    const before = index.getTokens();
    const patches = index.update(lines.join(''));
    const patched = applyPatches(before, patches);
    assertDeepEqual(patched, index.getTokens(), `fuzz step ${step}`);
    assertWellFormed(patched);
  }
});
