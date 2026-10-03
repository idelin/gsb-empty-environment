import { test } from "node:test";
import assert from "node:assert/strict";
import { BitReader, BitWriter } from "../src/bitstream.ts";
import {
  buildTable,
  countSymbols,
  normalizeFrequencies,
} from "../src/table.ts";
import { decodeAll, encodeAll, TansDecoder, TansEncoder } from "../src/tans.ts";
import { compress, decompress, shannonEntropy } from "../src/codec.ts";
import {
  ArithmeticDecoder,
  ArithmeticEncoder,
  cumulative,
} from "../src/arithmetic.ts";

function makeRng(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 0x100000000;
  };
}

/** Highly skewed geometric-ish distribution over `alphabet` symbols. */
function skewedSample(n: number, alphabet: number, skew: number, seed = 42): Uint8Array {
  const rng = makeRng(seed);
  const weights = Array.from({ length: alphabet }, (_, i) => Math.pow(skew, i));
  const total = weights.reduce((a, b) => a + b, 0);
  const cum = weights.map((_, i) => weights.slice(0, i + 1).reduce((a, b) => a + b, 0) / total);
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) {
    const r = rng();
    let s = 0;
    while (s < alphabet - 1 && r > cum[s]) s++;
    out[i] = s;
  }
  return out;
}

test("bitstream: backward write/read roundtrip", () => {
  const w = new BitWriter();
  const chunks: [number, number][] = [[5, 3], [0, 0], [1023, 10], [7, 3], [0xffffffff, 32], [1, 1]];
  for (const [v, n] of chunks) w.write(v, n);
  const { bytes, bitLength } = w.finish();
  assert.equal(bitLength, 3 + 10 + 3 + 32 + 1);
  const r = new BitReader(bytes, bitLength);
  for (let i = chunks.length - 1; i >= 0; i--) {
    const [v, n] = chunks[i];
    assert.equal(r.read(n), v >>> 0);
  }
  assert.equal(r.remaining, 0);
});

test("bitstream: forward read matches write order", () => {
  const w = new BitWriter();
  const chunks: [number, number][] = [[0b101, 3], [0b1111111, 7], [0xabcd, 16]];
  for (const [v, n] of chunks) w.write(v, n);
  const { bytes, bitLength } = w.finish();
  const r = new BitReader(bytes, bitLength);
  for (const [v, n] of chunks) assert.equal(r.readForward(n), v);
});

test("table: frequencies normalise to exactly 2^tableLog, min 1", () => {
  const counts = new Map([[0, 90000], [1, 9000], [2, 900], [3, 99], [4, 1]]);
  for (const log of [8, 10, 12]) {
    const freq = normalizeFrequencies(counts, log);
    let total = 0;
    for (const f of freq) total += f;
    assert.equal(total, 1 << log);
    for (const s of counts.keys()) assert.ok(freq[s] >= 1);
  }
});

test("tans: push/pop roundtrip on skewed sample", () => {
  const data = skewedSample(100_000, 16, 0.5);
  const table = buildTable(normalizeFrequencies(countSymbols(data), 12), 12);
  const { bytes, bitLength } = encodeAll(table, data);
  const decoded = decodeAll(table, bytes, bitLength, data.length);
  assert.deepEqual(decoded, data);
});

test("tans: state stays within [M, 2M) — single big-integer state", () => {
  const data = skewedSample(50_000, 64, 0.7, 7);
  const table = buildTable(normalizeFrequencies(countSymbols(data), 11), 11);
  const w = new BitWriter();
  const enc = new TansEncoder(table);
  for (let i = data.length - 1; i >= 0; i--) {
    enc.push(data[i], w);
    const st = enc.currentState;
    assert.ok(st >= table.size && st < 2 * table.size, `state ${st} out of range`);
  }
});

test("tans: encode/decode are exact inverses for every state x symbol", () => {
  // Exhaustive proof of the state-transition table: for every symbol and
  // every reachable state, decode(encode(x, s)) must return (x, s) and the
  // emitted bits must be read back exactly.
  const counts = new Map([[0, 700], [1, 200], [2, 90], [3, 9], [4, 1]]);
  const tableLog = 10;
  const table = buildTable(normalizeFrequencies(counts, tableLog), tableLog);
  const M = table.size;
  for (const s of table.symbols) {
    for (let x = M; x < 2 * M; x++) {
      const w = new BitWriter();
      const enc = new TansEncoder(table);
      (enc as unknown as { state: number }).state = x;
      enc.push(s, w);
      enc.finish(w); // appends the post-encode state for the decoder to start from
      const packed = w.finish();
      const dec = new TansDecoder(table, new BitReader(packed.bytes, packed.bitLength));
      assert.equal(dec.pop(), s, `symbol mismatch at x=${x} s=${s}`);
      assert.equal(
        (dec as unknown as { state: number }).state,
        x,
        `state mismatch at x=${x} s=${s}`,
      );
    }
  }
});

test("tans: compression approaches Shannon bound on skewed input", () => {
  const data = skewedSample(500_000, 32, 0.6, 1234);
  const table = buildTable(normalizeFrequencies(countSymbols(data), 12), 12);
  const { bitLength } = encodeAll(table, data);
  const entropy = shannonEntropy(data);
  const achieved = bitLength / data.length;
  // tANS payload must sit within a small additive overhead of the entropy bound
  assert.ok(achieved >= entropy, `below entropy? ${achieved} < ${entropy}`);
  assert.ok(
    achieved < entropy + 0.02,
    `payload ${achieved.toFixed(4)} bits/sym vs entropy ${entropy.toFixed(4)}`,
  );
});

test("codec: full container roundtrip incl. header", () => {
  const data = skewedSample(200_000, 24, 0.55, 99);
  const c = compress(data, 12);
  assert.ok(c.ratio > 1.5, `expected real compression, got ratio ${c.ratio}`);
  const d = decompress(c.bytes);
  assert.deepEqual(d, data);
  // container-level overhead stays within 1% of entropy for large skewed input
  assert.ok(c.achievedBitsPerSymbol < c.entropyBitsPerSymbol * 1.01 + 0.01);
});

test("codec: edge cases", () => {
  // single distinct symbol
  const ones = new Uint8Array(10_000).fill(7);
  assert.deepEqual(decompress(compress(ones).bytes), ones);
  // length 1
  assert.deepEqual(decompress(compress(new Uint8Array([200])).bytes), new Uint8Array([200]));
  // full 256-symbol alphabet, uniform random (incompressible — must still roundtrip)
  const rng = makeRng(5);
  const uniform = new Uint8Array(50_000).map(() => (rng() * 256) | 0);
  assert.deepEqual(decompress(compress(uniform, 12).bytes), uniform);
  // text-like input
  const text = new TextEncoder().encode(
    "the quick brown fox jumps over the lazy dog. ".repeat(2000),
  );
  const ct = compress(text);
  assert.deepEqual(decompress(ct.bytes), text);
  assert.ok(ct.ratio > 1.5);
});

test("codec: deterministic output", () => {
  const data = skewedSample(10_000, 8, 0.5, 3);
  assert.deepEqual(compress(data).bytes, compress(data).bytes);
});

test("arithmetic baseline: roundtrip with same model", () => {
  const data = skewedSample(20_000, 16, 0.6, 11);
  const freq = normalizeFrequencies(countSymbols(data), 12);
  const cum = cumulative(freq);
  const total = 1 << 12;

  const w = new BitWriter();
  const enc = new ArithmeticEncoder(cum, total, w);
  for (const b of data) enc.encode(b);
  enc.finish();
  const { bytes, bitLength } = w.finish();

  const dec = new ArithmeticDecoder(cum, total, new BitReader(bytes, bitLength), 256);
  const out = new Uint8Array(data.length);
  for (let i = 0; i < data.length; i++) out[i] = dec.decode();
  assert.deepEqual(out, data);

  // tANS payload on the same model must be no larger than arithmetic + tiny slack
  const table = buildTable(freq, 12);
  const tansBits = encodeAll(table, data).bitLength;
  assert.ok(tansBits <= bitLength * 1.01, "tANS should match arithmetic coder size");
});

test("tans: interleaved encoder/decoder manual walk", () => {
  const data = new Uint8Array([3, 1, 4, 1, 5, 9, 2, 6]);
  const counts = countSymbols(data);
  const table = buildTable(normalizeFrequencies(counts, 8), 8);
  const w = new BitWriter();
  const enc = new TansEncoder(table);
  for (let i = data.length - 1; i >= 0; i--) enc.push(data[i], w);
  const { bytes, bitLength } = enc.finish(w);
  const dec = new TansDecoder(table, new BitReader(bytes, bitLength));
  for (let i = 0; i < data.length; i++) assert.equal(dec.pop(), data[i]);
});
