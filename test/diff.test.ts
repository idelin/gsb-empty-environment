import test from 'node:test';
import assert from 'node:assert/strict';
import { diffTokens, diffTokensExhaustive, verifyDiff } from '../src/index.ts';
import type { DiffResult } from '../src/index.ts';

const toks = (s: string): string[] => (s === '' ? [] : s.split(' '));

function assertValid(oldT: string[], newT: string[], result: DiffResult): void {
  const v = verifyDiff(oldT, newT, result);
  assert.ok(v.valid, `expected valid diff, errors:\n${v.errors.join('\n')}`);
}

test('identical sequences: everything kept, no blocks', () => {
  const t = toks('a b c d');
  const r = diffTokens(t, t);
  assertValid(t, t, r);
  assert.equal(r.stats.kept, 4);
  assert.equal(r.stats.moved, 0);
  assert.equal(r.stats.blockCount, 0);
  assert.ok(r.oldTokens.every((x) => x.status === 'kept'));
});

test('empty inputs', () => {
  const r = diffTokens([], []);
  assertValid([], [], r);
  assert.equal(r.stats.kept, 0);
  const r2 = diffTokens([], toks('a b'));
  assertValid([], toks('a b'), r2);
  assert.equal(r2.stats.inserted, 2);
  const r3 = diffTokens(toks('a b'), []);
  assertValid(toks('a b'), [], r3);
  assert.equal(r3.stats.deleted, 2);
});

test('disjoint sequences: all deleted + inserted, no moves', () => {
  const oldT = toks('a b c');
  const newT = toks('x y z');
  const r = diffTokens(oldT, newT);
  assertValid(oldT, newT, r);
  assert.equal(r.stats.deleted, 3);
  assert.equal(r.stats.inserted, 3);
  assert.equal(r.stats.blockCount, 0);
});

test('simple block move is detected as one block', () => {
  const oldT = toks('a b c d');
  const newT = toks('c d a b');
  const r = diffTokens(oldT, newT);
  assertValid(oldT, newT, r);
  assert.equal(r.stats.kept, 2);
  assert.equal(r.stats.moved, 2);
  assert.equal(r.stats.blockCount, 1);
  const block = r.blocks[0];
  assert.deepEqual([block.oldStart, block.oldEnd], [0, 1]);
  assert.deepEqual([block.newStart, block.newEnd], [2, 3]);
  assert.equal(r.oldTokens[0].status, 'moved');
  assert.equal(r.oldTokens[1].status, 'moved');
  assert.equal(r.newTokens[0].status, 'kept');
});

test('stable origin labels: matched tokens share labels across sides', () => {
  const oldT = toks('a b c');
  const newT = toks('c a b');
  const r = diffTokens(oldT, newT);
  assertValid(oldT, newT, r);
  const oldLabels = new Set(r.oldTokens.map((t) => t.label));
  for (const t of r.newTokens) {
    if (t.status !== 'inserted') assert.ok(oldLabels.has(t.label));
  }
  assert.deepEqual(
    r.oldTokens.map((t) => t.label),
    ['O0', 'O1', 'O2'],
  );
});

test('repeated blocks: duplicated tokens get a deterministic, verifiable plan', () => {
  const oldT = toks('x a b x a b');
  const newT = toks('a b x a b x');
  const r = diffTokens(oldT, newT);
  assertValid(oldT, newT, r);
  const r2 = diffTokens(oldT, newT);
  assert.deepEqual(r, r2, 'diff must be deterministic');

  const oldT2 = toks('a b a b');
  const newT2 = toks('b a b a');
  const r3 = diffTokens(oldT2, newT2);
  assertValid(oldT2, newT2, r3);
  // All 4 tokens are preserved; the backbone keeps 2, the other 2 move.
  assert.equal(r3.stats.kept, 2);
  assert.equal(r3.stats.moved, 2);
  assert.equal(r3.stats.deleted, 0);
  assert.equal(r3.stats.inserted, 0);
  assert.equal(r3.stats.blockCount, 2);
});

test('small edits inside a moved block stay one block', () => {
  // "x" deleted and "NEW" inserted inside the moved [a b] region.
  const oldT = toks('a x b c d');
  const newT = toks('c d a NEW b');
  const r = diffTokens(oldT, newT);
  assertValid(oldT, newT, r);
  assert.equal(r.stats.blockCount, 1);
  assert.equal(r.stats.deleted, 1);
  assert.equal(r.stats.inserted, 1);
  const block = r.blocks[0];
  // Old span covers the deleted token "x" (index 1) inside the block.
  assert.deepEqual([block.oldStart, block.oldEnd], [0, 2]);
  // New span covers the inserted token "NEW" (index 3) inside the block.
  assert.deepEqual([block.newStart, block.newEnd], [2, 4]);
  assert.equal(block.size, 2);
  assert.equal(r.oldTokens[1].status, 'deleted');
  assert.equal(r.newTokens[3].status, 'inserted');
});

test('nested candidates: inner move preferred over splitting outer block', () => {
  // [c d] could move before [a b], or [a b] after [c d]; e anchors the end.
  const oldT = toks('a b c d e');
  const newT = toks('c d a b e');
  const r = diffTokens(oldT, newT);
  assertValid(oldT, newT, r);
  assert.equal(r.stats.kept, 3);
  assert.equal(r.stats.blockCount, 1);
  // Tie-break by source position: moving the earlier-source block [a b] wins.
  assert.deepEqual(
    r.oldTokens.filter((t) => t.status === 'moved').map((t) => t.text),
    ['a', 'b'],
  );

  // Swapped pairs: two independent single-token moves, no merge possible.
  const oldT2 = toks('a b c d');
  const newT2 = toks('b a d c');
  const r2 = diffTokens(oldT2, newT2);
  assertValid(oldT2, newT2, r2);
  assert.equal(r2.stats.kept, 2);
  assert.equal(r2.stats.blockCount, 2);
});

test('budget exhaustion returns a verifiable greedy plan', () => {
  const oldT = toks('a b c d e f g h i j k l');
  const newT = toks('g h i j k l a b c d e f');
  for (const budget of [0, 1, 5]) {
    const r = diffTokens(oldT, newT, { budget });
    assert.equal(r.stats.budgetExhausted, true);
    assert.equal(r.stats.optimal, false);
    assertValid(oldT, newT, r);
    // Greedy fallback still keeps the LIS backbone (primary objective intact).
    assert.equal(r.stats.kept, 6);
  }
  const full = diffTokens(oldT, newT);
  assert.equal(full.stats.optimal, true);
  assert.equal(full.stats.blockCount, 1);
  assertValid(oldT, newT, full);
});

test('verifyDiff rejects tampered results', () => {
  const oldT = toks('a b c d');
  const newT = toks('c d a b');
  const r = diffTokens(oldT, newT);
  assertValid(oldT, newT, r);

  const tampered: DiffResult = JSON.parse(JSON.stringify(r));
  tampered.oldTokens[0].status = 'kept';
  tampered.oldTokens[0].blockId = null;
  const v1 = verifyDiff(oldT, newT, tampered);
  assert.equal(v1.valid, false);

  const tampered2: DiffResult = JSON.parse(JSON.stringify(r));
  tampered2.blocks = [];
  const v2 = verifyDiff(oldT, newT, tampered2);
  assert.equal(v2.valid, false);

  const tampered3: DiffResult = JSON.parse(JSON.stringify(r));
  tampered3.stats.blockCount = 7;
  const v3 = verifyDiff(oldT, newT, tampered3);
  assert.equal(v3.valid, false);
});

// Deterministic PRNG (mulberry32) for reproducible randomized checks.
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('exhaustive search confirms DP optimality on small random inputs', () => {
  const rand = rng(20261005);
  const alphabet = ['a', 'b', 'c', 'd'];
  const cases = 300;
  for (let iter = 0; iter < cases; iter++) {
    const n = Math.floor(rand() * 9);
    const m = Math.floor(rand() * 9);
    const oldT = Array.from({ length: n }, () => alphabet[Math.floor(rand() * alphabet.length)]);
    const newT = Array.from({ length: m }, () => alphabet[Math.floor(rand() * alphabet.length)]);

    const fast = diffTokens(oldT, newT);
    const brute = diffTokensExhaustive(oldT, newT);
    assertValid(oldT, newT, fast);
    assertValid(oldT, newT, brute);
    assert.equal(fast.stats.kept, brute.stats.kept, `kept mismatch: ${oldT} -> ${newT}`);
    assert.equal(fast.stats.blockCount, brute.stats.blockCount, `blocks mismatch: ${oldT} -> ${newT}`);
    assert.equal(
      fast.stats.movedSourceSum,
      brute.stats.movedSourceSum,
      `tie-break mismatch: ${oldT} -> ${newT}`,
    );
  }
});

test('exhaustive search confirms optimality on structured move cases', () => {
  const fixtures: Array<[string, string]> = [
    ['a b c d', 'c d a b'],
    ['a b c d e', 'c d a b e'],
    ['a b c d', 'b a d c'],
    ['a x b c d', 'c d a NEW b'],
    ['x a b x a b', 'a b x a b x'],
    ['a b a b', 'b a b a'],
    ['p q a b c r s', 'a b c p q r s'],
    ['a b c', 'c b a'],
  ];
  for (const [o, n] of fixtures) {
    const oldT = toks(o);
    const newT = toks(n);
    const fast = diffTokens(oldT, newT);
    const brute = diffTokensExhaustive(oldT, newT);
    assertValid(oldT, newT, fast);
    assert.equal(fast.stats.kept, brute.stats.kept, `${o} -> ${n}`);
    assert.equal(fast.stats.blockCount, brute.stats.blockCount, `${o} -> ${n}`);
    assert.equal(fast.stats.movedSourceSum, brute.stats.movedSourceSum, `${o} -> ${n}`);
  }
});
