import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { applyOp, applyOps, transform } from '../src/ot.ts';
import type { TextOp } from '../src/ot.ts';

// Deterministic PRNG (mulberry32) so failures are reproducible.
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

const ALPHABET = ['a', 'b', 'c', '\n', '\r\n', '\uD83D\uDE00', '\u4e2d'];

function randomDoc(rand: () => number): string {
  const n = Math.floor(rand() * 12);
  let s = '';
  for (let i = 0; i < n; i++) s += ALPHABET[Math.floor(rand() * ALPHABET.length)];
  return s;
}

/** Random boundary that never splits a surrogate pair. */
function randomBoundary(rand: () => number, doc: string): number {
  const spots: number[] = [];
  for (let i = 0; i <= doc.length; i++) {
    const splits =
      i > 0 &&
      i < doc.length &&
      doc.charCodeAt(i - 1) >= 0xd800 &&
      doc.charCodeAt(i - 1) <= 0xdbff &&
      doc.charCodeAt(i) >= 0xdc00 &&
      doc.charCodeAt(i) <= 0xdfff;
    if (!splits) spots.push(i);
  }
  return spots[Math.floor(rand() * spots.length)];
}

function randomOp(rand: () => number, doc: string): TextOp {
  if (rand() < 0.5 || doc.length === 0) {
    const pos = randomBoundary(rand, doc);
    const n = Math.floor(rand() * 3) + 1;
    let text = '';
    for (let i = 0; i < n; i++) text += ALPHABET[Math.floor(rand() * ALPHABET.length)];
    return { kind: 'insert', pos, text };
  }
  const pos = randomBoundary(rand, doc);
  const ends: number[] = [];
  for (let e = pos; e <= doc.length; e++) {
    const splits =
      e > 0 &&
      e < doc.length &&
      doc.charCodeAt(e - 1) >= 0xd800 &&
      doc.charCodeAt(e - 1) <= 0xdbff &&
      doc.charCodeAt(e) >= 0xdc00 &&
      doc.charCodeAt(e) <= 0xdfff;
    if (!splits) ends.push(e);
  }
  const end = ends[Math.floor(rand() * ends.length)];
  return { kind: 'delete', pos, len: end - pos };
}

describe('fuzz: TP1 convergence and length legality', () => {
  it('500 random concurrent op pairs converge in both delivery orders', () => {
    const rand = rng(20260924);
    for (let i = 0; i < 500; i++) {
      const doc = randomDoc(rand);
      const opA = randomOp(rand, doc);
      const opB = randomOp(rand, doc);
      // Concurrent ops always come from distinct sites (session precondition).
      const swap = rand() < 0.5;
      const siteA = swap ? 2 : 1;
      const siteB = swap ? 1 : 2;
      // Both orders must apply without throwing (offsets stay legal)
      // and must produce identical documents.
      const left = applyOps(applyOp(doc, opA), transform(opB, siteB, opA, siteA));
      const right = applyOps(applyOp(doc, opB), transform(opA, siteA, opB, siteB));
      assert.equal(
        left,
        right,
        `diverged at #${i}: doc=${JSON.stringify(doc)} ` +
          `A=${JSON.stringify(opA)}@${siteA} B=${JSON.stringify(opB)}@${siteB}`,
      );
    }
  });
});
