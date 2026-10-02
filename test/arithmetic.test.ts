import { test } from 'node:test';
import assert from 'node:assert/strict';
import { encode, decode, binaryEntropy } from '../src/arithmetic.ts';

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomBinary(n: number, pZero: number, seed: number): Uint8Array {
  const rand = mulberry32(seed);
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) out[i] = rand() < pZero ? 0 : 1;
  return out;
}

function assertRoundTrip(symbols: Uint8Array): Buffer {
  const compressed = encode(symbols);
  const restored = decode(compressed);
  assert.equal(restored.length, symbols.length, 'decoded length must match exactly');
  assert.deepEqual([...restored], [...symbols], 'round-trip must be lossless');
  return compressed;
}

test('round-trips binary streams of many lengths (exact termination)', () => {
  for (const n of [0, 1, 2, 3, 7, 8, 63, 64, 100, 1000, 10001]) {
    assertRoundTrip(randomBinary(n, 0.9, n * 7 + 1));
  }
});

test('round-trips degenerate streams (all 0s / all 1s)', () => {
  assertRoundTrip(new Uint8Array(5000).fill(0));
  assertRoundTrip(new Uint8Array(5000).fill(1));
});

test('round-trips across the full bias range', () => {
  for (const p of [0.01, 0.1, 0.3, 0.5, 0.7, 0.9, 0.99]) {
    assertRoundTrip(randomBinary(20000, p, 1234));
  }
});

test('acceptance: 90%-zero stream compresses far below the Huffman lower bound', () => {
  const n = 200_000;
  const symbols = randomBinary(n, 0.9, 99);
  const compressed = assertRoundTrip(symbols);

  // Huffman on a binary alphabet needs >= 1 bit/symbol -> >= n/8 bytes.
  const huffmanBoundBytes = Math.ceil(n / 8);
  assert.ok(
    compressed.length < huffmanBoundBytes * 0.6,
    `expected << Huffman bound ${huffmanBoundBytes} bytes, got ${compressed.length}`,
  );

  // ...and approaches the Shannon entropy limit (~0.469 bits/symbol for p=0.9).
  const entropyBytes = (binaryEntropy(0.9) * n) / 8;
  assert.ok(
    compressed.length < entropyBytes * 1.05,
    `expected within 5% of entropy limit ${Math.ceil(entropyBytes)} bytes, got ${compressed.length}`,
  );
});

test('compression ratio tracks entropy for several biases', () => {
  const n = 100_000;
  for (const p of [0.5, 0.7, 0.9, 0.99]) {
    const compressed = assertRoundTrip(randomBinary(n, p, 7));
    const entropyBytes = (binaryEntropy(p) * n) / 8;
    // Adaptive model warm-up costs a small constant overhead.
    assert.ok(compressed.length < entropyBytes + n * 0.01 + 64,
      `p=${p}: ${compressed.length} bytes vs entropy ${Math.ceil(entropyBytes)}`);
  }
});

test('decoding is deterministic and independent of trailing padding bits', () => {
  const symbols = randomBinary(4096, 0.9, 5);
  const compressed = encode(symbols);
  const first = decode(compressed);
  const second = decode(compressed);
  assert.deepEqual([...first], [...second]);
  assert.deepEqual([...first], [...symbols]);
});
