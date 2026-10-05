import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  assignCanonicalCodes,
  buildCodeLengths,
  decodeSymbol,
  makeDecoder,
  validateCodeLengths,
} from '../src/huffman.ts';
import { BitReader, BitWriter } from '../src/bitstream.ts';
import { BlockError } from '../src/format.ts';
import { crc32 } from '../src/crc32.ts';

test('canonical codes: assigned by (length, symbol)', () => {
  // lengths: sym0=1, sym1=2, sym2=3, sym3=3 -> codes 0, 10, 110, 111
  const lens = new Uint8Array([1, 2, 3, 3]);
  const codes = assignCanonicalCodes(lens);
  assert.equal(codes[0], 0b0);
  assert.equal(codes[1], 0b10);
  assert.equal(codes[2], 0b110);
  assert.equal(codes[3], 0b111);
});

test('canonical codes: shorter symbols sort before longer, ties by symbol', () => {
  // sym2 gets length 1, sym0 and sym1 get length 2 -> sym2=0, sym0=10, sym1=11
  const lens = new Uint8Array([2, 2, 1]);
  const codes = assignCanonicalCodes(lens);
  assert.equal(codes[2], 0b0);
  assert.equal(codes[0], 0b10);
  assert.equal(codes[1], 0b11);
});

test('huffman round trip through bit stream', () => {
  const freqs = [5, 9, 12, 13, 16, 45];
  const lens = buildCodeLengths(freqs);
  validateCodeLengths(lens, { allowEmpty: false });
  const codes = assignCanonicalCodes(lens);
  const dec = makeDecoder(lens);
  const w = new BitWriter();
  const sequence = [0, 5, 3, 3, 2, 4, 1, 0, 0, 5];
  for (const s of sequence) w.writeCode(codes[s], lens[s]);
  const reader = new BitReader(w.finish());
  for (const s of sequence) assert.equal(decodeSymbol(dec, reader), s);
});

test('oversubscribed code lengths are rejected', () => {
  const lens = new Uint8Array([1, 1, 1]); // Kraft sum 1.5
  assert.throws(
    () => validateCodeLengths(lens, { allowEmpty: false }),
    (e: unknown) => e instanceof BlockError && e.code === 'OVERSUBSCRIBED',
  );
});

test('incomplete and single-symbol codes are legal', () => {
  validateCodeLengths(new Uint8Array([1]), { allowEmpty: false }); // single symbol
  validateCodeLengths(new Uint8Array([2, 2, 2]), { allowEmpty: false }); // incomplete
  validateCodeLengths(new Uint8Array([0, 0]), { allowEmpty: true }); // empty distance table
  assert.throws(
    () => validateCodeLengths(new Uint8Array([0, 0]), { allowEmpty: false }),
    (e: unknown) => e instanceof BlockError && e.code === 'CORRUPT',
  );
});

test('code lengths are limited to 15 bits even for skewed frequencies', () => {
  // Fibonacci frequencies force a deep Huffman tree.
  const freqs: number[] = [1, 1];
  while (freqs.length < 30) freqs.push(freqs[freqs.length - 1] + freqs[freqs.length - 2]);
  const lens = buildCodeLengths(freqs);
  for (const l of lens) assert.ok(l <= 15, `length ${l} exceeds limit`);
  validateCodeLengths(lens, { allowEmpty: false });
  // Every symbol still round-trips.
  const codes = assignCanonicalCodes(lens);
  const dec = makeDecoder(lens);
  for (let s = 0; s < freqs.length; s++) {
    const w = new BitWriter();
    w.writeCode(codes[s], lens[s]);
    assert.equal(decodeSymbol(dec, new BitReader(w.finish())), s);
  }
});

test('single used symbol gets a one-bit code', () => {
  const lens = buildCodeLengths([0, 0, 7, 0]);
  assert.equal(lens[2], 1);
  const codes = assignCanonicalCodes(lens);
  assert.equal(codes[2], 0);
  const dec = makeDecoder(lens);
  const w = new BitWriter();
  w.writeCode(codes[2], lens[2]);
  assert.equal(decodeSymbol(dec, new BitReader(w.finish())), 2);
});

test('crc32 known vector', () => {
  assert.equal(crc32(new TextEncoder().encode('123456789')), 0xcbf43926);
  assert.equal(crc32(new Uint8Array(0)), 0);
});
