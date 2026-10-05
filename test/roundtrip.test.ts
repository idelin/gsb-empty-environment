import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compress, decompress } from '../src/api.ts';
import { decodeBlockTokens, inspectBlocks } from '../src/block.ts';
import { expandTokens } from '../src/reference.ts';

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

function* allStrings(alphabet: number[], maxLen: number, prefix: number[] = []): Generator<number[]> {
  yield prefix;
  if (prefix.length === maxLen) return;
  for (const c of alphabet) {
    yield* allStrings(alphabet, maxLen, [...prefix, c]);
  }
}

test('exhaustive small-string round trip (ternary alphabet, len <= 6)', () => {
  let count = 0;
  for (const s of allStrings([97, 98, 99], 6)) {
    const data = Uint8Array.from(s);
    const packed = compress(data, { windowBits: 4 });
    assert.deepEqual(decompress(packed), data, `stream path failed for [${s}]`);
    // Reference path: decode to tokens, expand without any compressor logic.
    const { tokens, header } = decodeBlockTokens(packed);
    assert.deepEqual(expandTokens(tokens, header.origLen), data, `token path failed for [${s}]`);
    count++;
  }
  assert.equal(count, 1 + 3 + 9 + 27 + 81 + 243 + 729);
});

test('exhaustive small-string round trip (binary alphabet, len <= 8)', () => {
  let count = 0;
  for (const s of allStrings([0x00, 0xff], 8)) {
    const data = Uint8Array.from(s);
    const packed = compress(data, { windowBits: 4 });
    assert.deepEqual(decompress(packed), data, `stream path failed for [${s}]`);
    const { tokens, header } = decodeBlockTokens(packed);
    assert.deepEqual(expandTokens(tokens, header.origLen), data, `token path failed for [${s}]`);
    count++;
  }
  assert.equal(count, 511);
});

test('distance-1 long repetition uses overlapping copy', () => {
  const data = new Uint8Array(100000).fill(0x78);
  const packed = compress(data);
  const { tokens, header } = decodeBlockTokens(packed);
  const match = tokens.find((t) => t.type === 'match' && t.distance === 1);
  assert.ok(match, 'expected a distance-1 match');
  assert.ok(match.type === 'match' && match.length > match.distance, 'length must exceed distance');
  assert.deepEqual(decompress(packed), data);
  assert.deepEqual(expandTokens(tokens, header.origLen), data);
});

test('empty input produces one empty block', () => {
  const packed = compress(new Uint8Array(0));
  const infos = inspectBlocks(packed);
  assert.equal(infos.length, 1);
  assert.equal(infos[0].origLen, 0);
  const { tokens } = decodeBlockTokens(packed);
  assert.equal(tokens.length, 0);
  assert.deepEqual(decompress(packed), new Uint8Array(0));
  // An empty stream (zero blocks) also decodes to empty.
  assert.deepEqual(decompress(new Uint8Array(0)), new Uint8Array(0));
});

test('single-symbol inputs', () => {
  for (const n of [1, 2, 3, 5000]) {
    const data = new Uint8Array(n).fill(0x61);
    const packed = compress(data, { windowBits: 8 });
    assert.deepEqual(decompress(packed), data, `length ${n}`);
    const { tokens, header } = decodeBlockTokens(packed);
    assert.deepEqual(expandTokens(tokens, header.origLen), data, `token path length ${n}`);
  }
});

test('random (near-incompressible) data round trip', () => {
  const rand = mulberry32(42);
  const data = new Uint8Array(65536).map(() => Math.floor(rand() * 256));
  const packed = compress(data);
  assert.deepEqual(decompress(packed), data);
});

test('multi-block stream round trip', () => {
  const rand = mulberry32(7);
  const data = new Uint8Array(200000).map((_, i) =>
    i % 97 < 60 ? 0x41 + (i % 26) : Math.floor(rand() * 256),
  );
  const packed = compress(data, { blockSize: 65536 });
  assert.equal(inspectBlocks(packed).length, 4);
  assert.deepEqual(decompress(packed), data);
});

test('text-like repetitive data round trip at several window sizes', () => {
  const line = new TextEncoder().encode('2026-10-05T08:00:00 INFO worker-7 processed request id=12345 status=ok\n');
  const data = new Uint8Array(line.length * 500);
  for (let i = 0; i < 500; i++) data.set(line, i * line.length);
  for (const windowBits of [4, 8, 12, 16, 20]) {
    const packed = compress(data, { windowBits });
    assert.deepEqual(decompress(packed), data, `windowBits ${windowBits}`);
  }
});
