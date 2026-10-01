import { strict as assert } from "node:assert";
import { test } from "./harness.js";
import { BitWriter, BitReader } from "../src/bitstream.js";
import { FrequencyModel } from "../src/model.js";
import { compress, decompress } from "../src/codec.js";
import { PRECISION } from "../src/arithmetic.js";

type RandFn = () => number;

function xorshift32(seed: number): RandFn {
  let s: number = (seed >>> 0) || 1;
  return function (): number {
    s ^= s << 13;
    s >>>= 0;
    s ^= s >> 17;
    s ^= s << 5;
    s >>>= 0;
    return s;
  };
}

// Binary symbol stream: 0 with probability p0, 1 with probability 1 - p0.
function genBits(n: number, p0: number, seed: number): Uint8Array {
  const rand: RandFn = xorshift32(seed);
  const out: Uint8Array = new Uint8Array(n);
  for (let i: number = 0; i < n; i++) {
    out[i] = rand() / 4294967296 < p0 ? 0 : 1;
  }
  return out;
}

function h2(p: number): number {
  if (p <= 0 || p >= 1) {
    return 0;
  }
  return -p * Math.log2(p) - (1 - p) * Math.log2(1 - p);
}

function concat(a: Uint8Array, b: Uint8Array): Uint8Array {
  const out: Uint8Array = new Uint8Array(a.length + b.length);
  out.set(a, 0);
  out.set(b, a.length);
  return out;
}

const BINARY_MODEL: FrequencyModel = new FrequencyModel([0, 1], [9, 1]);

test("bitstream: bit-level write/read roundtrip incl. partial byte", () => {
  const bits: number[] = [0, 1, 1, 0, 1, 0, 0, 1, 1, 1, 0, 0, 1];
  const writer: BitWriter = new BitWriter();
  for (const b of bits) {
    writer.writeBit(b);
  }
  assert.equal(writer.bitLength, bits.length);
  const reader: BitReader = new BitReader(writer.finish());
  for (const b of bits) {
    assert.equal(reader.readBit(), b);
  }
  assert.equal(reader.readBit(), 0); // zero padding past the end
});

test("model: cumulative distribution and binary search", () => {
  const model: FrequencyModel = new FrequencyModel([0, 1], [9, 1]);
  assert.deepEqual(model.cum, [0, 9, 10]);
  assert.equal(model.total, 10);
  assert.equal(model.indexOf(0), 0);
  assert.equal(model.indexOf(1), 1);
  assert.equal(model.findIndex(0), 0);
  assert.equal(model.findIndex(8), 0);
  assert.equal(model.findIndex(9), 1);
  assert.throws(() => model.indexOf(7));
});

test("codec: empty input roundtrip", () => {
  const packed: Uint8Array = compress(new Uint8Array(0), BINARY_MODEL);
  const back: Uint8Array = decompress(packed);
  assert.equal(back.length, 0);
});

test("codec: single repeated symbol needs ~zero payload bits", () => {
  const data: Uint8Array = new Uint8Array(100000); // all zeros
  const model: FrequencyModel = new FrequencyModel([0], [1]);
  const packed: Uint8Array = compress(data, model);
  assert.ok(packed.length <= 10, "packed length " + packed.length);
  assert.deepEqual(decompress(packed), data);
});

test("codec: roundtrip of 90%-zero binary streams at many sizes", () => {
  const sizes: number[] = [1, 2, 7, 100, 4096, 100000];
  for (const n of sizes) {
    const data: Uint8Array = genBits(n, 0.9, 1234 + n);
    const packed: Uint8Array = compress(data, BINARY_MODEL);
    assert.deepEqual(decompress(packed), data, "roundtrip n=" + n);
  }
});

test("codec: roundtrip with data-derived model", () => {
  const data: Uint8Array = genBits(50000, 0.9, 42);
  const model: FrequencyModel = FrequencyModel.fromData(data);
  const packed: Uint8Array = compress(data, model);
  assert.deepEqual(decompress(packed), data);
});

test("codec: beats the Huffman lower bound and approaches Shannon entropy", () => {
  const n: number = 100000;
  const data: Uint8Array = genBits(n, 0.9, 7);
  const packed: Uint8Array = compress(data, BINARY_MODEL);

  const entropyBits: number = n * h2(0.9); // ~0.4690 bits/symbol
  const huffmanBits: number = n; // iid binary Huffman can never beat 1 bit/symbol

  // Cannot beat the entropy limit (small slack for header + flush bits).
  assert.ok(packed.length * 8 >= entropyBits - 64,
    "below entropy limit: " + packed.length * 8 + " < " + entropyBits);
  // Within 5% + 64 bits of the entropy limit.
  assert.ok(packed.length * 8 <= entropyBits * 1.05 + 64,
    "not near entropy: " + packed.length * 8 + " vs " + entropyBits);
  // Significantly below the Huffman theoretical lower bound.
  assert.ok(packed.length * 8 < huffmanBits * 0.55,
    "not below Huffman bound: " + packed.length * 8 + " vs " + huffmanBits);
});

test("codec: decoder terminates exactly after symbolCount symbols", () => {
  const data: Uint8Array = genBits(10000, 0.9, 99);
  const packed: Uint8Array = compress(data, BINARY_MODEL);
  // Trailing garbage after the stream must not affect decoding: the symbol
  // count in the header defines the exact stopping point.
  const polluted: Uint8Array = concat(packed, Uint8Array.of(255, 255, 255));
  const back: Uint8Array = decompress(polluted);
  assert.equal(back.length, data.length);
  assert.deepEqual(back, data);
});

test("codec: encoding is deterministic", () => {
  const data: Uint8Array = genBits(20000, 0.9, 5);
  const a: Uint8Array = compress(data, BINARY_MODEL);
  const b: Uint8Array = compress(data, BINARY_MODEL);
  assert.deepEqual(a, b);
});

test("codec: generic byte alphabet roundtrip (text)", () => {
  const text: string = "the quick brown fox jumps over the lazy dog. ";
  const data: Uint8Array = new Uint8Array(text.length * 200);
  for (let i: number = 0; i < data.length; i++) {
    data[i] = text.charCodeAt(i % text.length);
  }
  const model: FrequencyModel = FrequencyModel.fromData(data);
  const packed: Uint8Array = compress(data, model);
  assert.deepEqual(decompress(packed), data);
  assert.ok(packed.length < data.length, "should compress redundant text");
});

test("codec: full 256-symbol alphabet roundtrip", () => {
  const rand: RandFn = xorshift32(2024);
  const data: Uint8Array = new Uint8Array(30000);
  for (let i: number = 0; i < data.length; i++) {
    data[i] = rand() & 255;
  }
  const model: FrequencyModel = FrequencyModel.fromData(data);
  const packed: Uint8Array = compress(data, model);
  assert.deepEqual(decompress(packed), data);
});

test("arithmetic: precision constant is large enough for the model", () => {
  assert.ok(PRECISION >= 64);
});
