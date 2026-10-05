import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compress } from '../src/api.ts';
import { StreamDecoder } from '../src/decoder.ts';
import { concatBytes } from '../src/format.ts';

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

function sampleData(): Uint8Array {
  const rand = mulberry32(99);
  const out = new Uint8Array(20000);
  for (let i = 0; i < out.length; i++) {
    out[i] = i % 5 === 0 ? Math.floor(rand() * 256) : 0x61 + (i % 3);
  }
  return out;
}

function drain(dec: StreamDecoder, quota: number): Uint8Array {
  const parts: Uint8Array[] = [];
  for (;;) {
    const chunk = dec.read(quota);
    if (chunk.length === 0) break;
    parts.push(chunk);
  }
  return concatBytes(parts);
}

test('input may be split at any byte boundary (two chunks)', () => {
  const data = sampleData();
  const stream = compress(data, { windowBits: 10, blockSize: 8192 });
  for (let i = 0; i <= stream.length; i++) {
    const dec = new StreamDecoder();
    dec.push(stream.subarray(0, i));
    dec.push(stream.subarray(i));
    dec.finish();
    assert.deepEqual(drain(dec, 1 << 16), data, `split at ${i}`);
  }
});

test('input may arrive in random-sized chunks', () => {
  const data = sampleData();
  const stream = compress(data, { windowBits: 10, blockSize: 4096 });
  const rand = mulberry32(1234);
  for (let trial = 0; trial < 50; trial++) {
    const dec = new StreamDecoder();
    let pos = 0;
    while (pos < stream.length) {
      const step = 1 + Math.floor(rand() * 97);
      dec.push(stream.subarray(pos, Math.min(pos + step, stream.length)));
      pos += step;
    }
    dec.finish();
    assert.deepEqual(drain(dec, 1 << 16), data, `trial ${trial}`);
  }
});

test('output quota of 1 byte: exact output, no duplicates after pause', () => {
  const data = sampleData();
  const stream = compress(data);
  const dec = new StreamDecoder();
  dec.push(stream);
  dec.finish();
  const parts: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const chunk = dec.read(1);
    if (chunk.length === 0) break;
    assert.equal(chunk.length, 1);
    total += chunk.length;
    assert.ok(total <= data.length, 'decoder produced more than the original: duplicate output');
    parts.push(chunk);
  }
  assert.equal(total, data.length);
  assert.deepEqual(concatBytes(parts), data);
});

test('quota pauses inside a long distance-1 match without repeating bytes', () => {
  const data = new Uint8Array(5000).fill(0x7a);
  const stream = compress(data);
  const dec = new StreamDecoder();
  dec.push(stream);
  dec.finish();
  assert.deepEqual(drain(dec, 7), data);
});

test('interleaved push and read', () => {
  const data = sampleData();
  const stream = compress(data, { blockSize: 4096 });
  const dec = new StreamDecoder();
  const parts: Uint8Array[] = [];
  let pos = 0;
  while (pos < stream.length) {
    dec.push(stream.subarray(pos, Math.min(pos + 333, stream.length)));
    pos += 333;
    parts.push(dec.read(1500));
  }
  dec.finish();
  parts.push(drain(dec, 1 << 16));
  assert.deepEqual(concatBytes(parts), data);
});

test('read(0) is a no-op and empty stream decodes to empty', () => {
  const dec = new StreamDecoder();
  assert.equal(dec.read(0).length, 0);
  dec.finish();
  assert.equal(dec.read(100).length, 0);
});
