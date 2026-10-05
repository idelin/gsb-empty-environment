import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computePatches, applyPatches, tokenKey } from '../src/diff.ts';
import { TokenType } from '../src/analyzer.ts';
import type { SemanticToken } from '../src/analyzer.ts';

function makeToken(index: number, salt: number): SemanticToken {
  const types = [
    TokenType.VariableDeclaration,
    TokenType.VariableReference,
    TokenType.UnresolvedReference,
  ];
  return {
    start: index * 10,
    end: index * 10 + 5,
    type: types[salt % types.length],
    binding: salt % 3 === 0 ? '' : `v${salt % 7}@${salt % 11}`,
  };
}

/** Deterministic PRNG (mulberry32) so failures are reproducible. */
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

function randomTokens(rand: () => number, length: number): SemanticToken[] {
  const tokens: SemanticToken[] = [];
  for (let i = 0; i < length; i++) {
    tokens.push(makeToken(i, Math.floor(rand() * 21)));
  }
  return tokens;
}

function mutate(rand: () => number, tokens: SemanticToken[]): SemanticToken[] {
  const result = tokens.slice();
  const edits = 1 + Math.floor(rand() * 4);
  for (let e = 0; e < edits; e++) {
    const kind = Math.floor(rand() * 3);
    if (kind === 0 && result.length > 0) {
      // delete a span
      const start = Math.floor(rand() * result.length);
      const count = 1 + Math.floor(rand() * Math.min(3, result.length - start));
      result.splice(start, count);
    } else if (kind === 1) {
      // insert fresh tokens
      const start = Math.floor(rand() * (result.length + 1));
      const count = 1 + Math.floor(rand() * 3);
      const inserted: SemanticToken[] = [];
      for (let i = 0; i < count; i++) {
        inserted.push(makeToken(start + i, Math.floor(rand() * 21)));
      }
      result.splice(start, 0, ...inserted);
    } else if (result.length > 0) {
      // replace a token in place
      const at = Math.floor(rand() * result.length);
      result[at] = makeToken(at, Math.floor(rand() * 21));
    }
  }
  return result;
}

test('patches applied to the old sequence always reproduce the new one', () => {
  const rand = rng(0x5eed);
  for (let round = 0; round < 500; round++) {
    const before = randomTokens(rand, Math.floor(rand() * 20));
    const after = mutate(rand, before);
    const patches = computePatches(before, after);

    for (let i = 0; i < patches.length; i++) {
      const patch = patches[i];
      assert.ok(patch.start <= patch.end, `round ${round}: degenerate interval`);
      assert.ok(patch.end <= before.length, `round ${round}: interval in range`);
      if (i > 0) {
        assert.ok(patch.start >= patches[i - 1].end, `round ${round}: sorted, non-overlapping`);
      }
    }

    assert.deepEqual(applyPatches(before, patches), after, `round ${round}: apply == target`);
  }
});

test('identical sequences produce no patches', () => {
  const tokens = randomTokens(rng(1), 10);
  assert.deepEqual(computePatches(tokens, tokens.slice()), []);
});

test('empty to non-empty is a single insertion patch', () => {
  const tokens = randomTokens(rng(2), 5);
  const patches = computePatches([], tokens);
  assert.equal(patches.length, 1);
  assert.deepEqual([patches[0].start, patches[0].end], [0, 0]);
  assert.deepEqual(patches[0].tokens, tokens);
});

test('non-empty to empty is a single deletion patch', () => {
  const tokens = randomTokens(rng(3), 5);
  const patches = computePatches(tokens, []);
  assert.equal(patches.length, 1);
  assert.deepEqual([patches[0].start, patches[0].end], [0, tokens.length]);
  assert.deepEqual(patches[0].tokens, []);
});

test('a single changed token yields exactly one minimal patch', () => {
  const before = randomTokens(rng(4), 8);
  const after = before.slice();
  after[3] = { ...after[3], binding: 'other@1' };
  const patches = computePatches(before, after);
  assert.equal(patches.length, 1);
  assert.deepEqual([patches[0].start, patches[0].end], [3, 4]);
  assert.deepEqual(patches[0].tokens, [after[3]]);
});

test('tokenKey distinguishes every field', () => {
  const base = makeToken(0, 1);
  const key = tokenKey(base);
  assert.notEqual(tokenKey({ ...base, start: base.start + 1 }), key);
  assert.notEqual(tokenKey({ ...base, end: base.end + 1 }), key);
  assert.notEqual(tokenKey({ ...base, type: TokenType.FunctionReference }), key);
  assert.notEqual(tokenKey({ ...base, binding: 'x@0' }), key);
});
