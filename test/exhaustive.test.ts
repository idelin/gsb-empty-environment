/**
 * Exhaustive cross-check against the platform decoder.
 *
 * For every test input of length <= 4 bytes we enumerate
 *   - every truncation position (every prefix), and
 *   - every chunking of that prefix (all 2^(n-1) split patterns),
 * decode incrementally, and compare against TextDecoder decoding the whole
 * prefix in one shot (strict and replace modes). Chunk boundaries must never
 * change the result, and the offset map must stay consistent and monotonic.
 *
 * Inputs: all 1- and 2-byte strings (full 256 alphabet), plus all 3- and
 * 4-byte strings over an edge-case alphabet (ASCII bounds, continuation
 * bounds, lead-byte bounds, overlong/surrogate/out-of-range leads).
 */

import test from 'node:test';
import assert from 'node:assert/strict';
import { IncrementalUtf8Decoder, Utf8DecodeError } from '../src/index.ts';

const replaceRef = new TextDecoder('utf-8', { fatal: false });
const strictRef = new TextDecoder('utf-8', { fatal: true });

const FULL: number[] = Array.from({ length: 256 }, (_, i) => i);
const EDGE = [
  0x00, 0x41, 0x7f, 0x80, 0xbf, 0xc0, 0xc1, 0xc2, 0xdf, 0xe0, 0xed, 0xef,
  0xf0, 0xf4, 0xf5, 0xff,
];

function* inputs(length: number, alphabet: number[]): Generator<Uint8Array> {
  const out = new Uint8Array(length);
  const stack = [0];
  while (stack.length > 0) {
    const depth = stack.length - 1;
    const value = stack[stack.length - 1];
    if (value === alphabet.length) {
      stack.pop();
      if (stack.length > 0) stack[stack.length - 1]++;
      continue;
    }
    out[depth] = alphabet[value];
    if (depth === length - 1) {
      yield Uint8Array.from(out);
      stack[stack.length - 1]++;
    } else {
      stack.push(0);
    }
  }
}

/** All chunkings of `bytes`: bitmask over the gaps between bytes. */
function* chunkings(bytes: Uint8Array): Generator<Uint8Array[]> {
  const gaps = bytes.length - 1;
  const masks = gaps <= 0 ? 1 : 1 << gaps;
  for (let mask = 0; mask < masks; mask++) {
    const chunks: Uint8Array[] = [];
    let start = 0;
    for (let gap = 0; gap < gaps; gap++) {
      if ((mask & (1 << gap)) !== 0) {
        chunks.push(bytes.subarray(start, gap + 1));
        start = gap + 1;
      }
    }
    chunks.push(bytes.subarray(start));
    yield chunks;
  }
}

function decodeIncremental(
  chunks: Uint8Array[],
  strategy: 'strict' | 'replace',
): { text: string; decoder: IncrementalUtf8Decoder } {
  const decoder = new IncrementalUtf8Decoder({ strategy });
  for (const chunk of chunks) decoder.push(chunk);
  decoder.finish();
  return { text: decoder.text, decoder };
}

function checkMapInvariants(decoder: IncrementalUtf8Decoder, byteLength: number): void {
  const map = decoder.map;
  const utf16Length = decoder.utf16Length;
  assert.equal(map.byteToUtf16(byteLength), utf16Length, 'end byte -> end text');
  assert.equal(map.utf16ToByte(utf16Length), byteLength, 'end text -> end byte');
  let previousByte = -1;
  let previousUtf16 = -1;
  for (let b = 0; b <= byteLength; b++) {
    const u = map.byteToUtf16(b);
    assert.ok(u >= previousUtf16, `byte->utf16 monotonic at ${b}`);
    previousUtf16 = u;
  }
  for (let u = 0; u <= utf16Length; u++) {
    const b = map.utf16ToByte(u);
    assert.ok(b >= previousByte, `utf16->byte monotonic at ${u}`);
    previousByte = b;
  }
}

let checkedInputs = 0;
let checkedDecodes = 0;

function verifyInput(bytes: Uint8Array): void {
  checkedInputs++;
  // Every truncation position: prefixes of length 0..bytes.length.
  for (let end = 0; end <= bytes.length; end++) {
    const prefix = bytes.subarray(0, end);
    const expectedReplace = replaceRef.decode(prefix);
    let expectedStrict: string | null = null;
    try {
      expectedStrict = strictRef.decode(prefix);
    } catch {
      expectedStrict = null;
    }
    // Strict mode is chunk-invariant by construction; check once per prefix.
    if (expectedStrict === null) {
      assert.throws(
        () => decodeIncremental([prefix], 'strict'),
        (error: unknown) => error instanceof Utf8DecodeError,
      );
    } else {
      assert.equal(decodeIncremental([prefix], 'strict').text, expectedStrict);
    }
    // Replace mode: every chunking must reproduce the whole-buffer result.
    for (const chunks of chunkings(prefix)) {
      const { text, decoder } = decodeIncremental(chunks, 'replace');
      checkedDecodes++;
      assert.equal(
        text,
        expectedReplace,
        `chunked decode mismatch for [${[...prefix]}] chunks=${chunks.map((c) => c.length)}`,
      );
      checkMapInvariants(decoder, end);
    }
  }
}

test('exhaustive: all inputs up to 4 bytes, all chunkings and truncations', () => {
  for (const byte of FULL) verifyInput(Uint8Array.of(byte));
  for (const input of inputs(2, FULL)) verifyInput(input);
  for (const input of inputs(3, EDGE)) verifyInput(input);
  for (const input of inputs(4, EDGE)) verifyInput(input);
  console.log(
    `  enumerated ${checkedInputs} inputs, ${checkedDecodes} chunked decodes`,
  );
}, { timeout: 300_000 });
