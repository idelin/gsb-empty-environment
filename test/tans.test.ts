import { test } from "node:test";
import assert from "node:assert/strict";
import {
  compress,
  decompress,
  buildTables,
  countFrequencies,
  normalizeCounts,
  TansEncoder,
  TansDecoder,
  BitWriter,
  ReverseBitReader,
} from "../src/index.ts";
import { mulberry32, sampleSkewed, skewedProbabilities } from "./util.ts";

function roundTrip(data: Uint8Array, tableLog = 12): Uint8Array {
  const blob = compress(data, tableLog);
  const back = decompress(blob);
  assert.deepEqual(back, data, `round-trip failed (len=${data.length}, tableLog=${tableLog})`);
  return blob;
}

test("table invariants: frequencies sum to table size, states stay in range", () => {
  const rand = mulberry32(1);
  for (let trial = 0; trial < 50; trial++) {
    const counts = new Uint32Array(256);
    const n = 1 + Math.floor(rand() * 40);
    for (let i = 0; i < n; i++) counts[Math.floor(rand() * 256)] += 1 + Math.floor(rand() * 1000);
    const tableLog = 8 + Math.floor(rand() * 8); // 8..15
    const norm = normalizeCounts(counts, tableLog);
    const tableSize = 1 << tableLog;

    let sum = 0;
    for (let s = 0; s < 256; s++) {
      sum += norm[s];
      if (counts[s] > 0) assert.ok(norm[s] >= 1, "present symbol needs freq >= 1");
      else assert.equal(norm[s], 0);
    }
    assert.equal(sum, tableSize);

    const t = buildTables(norm, tableLog);
    for (let u = 0; u < tableSize; u++) {
      assert.ok(t.dNewState[u] < tableSize);
      assert.ok(t.dNbBits[u] <= tableLog);
      assert.ok(t.nextState[u] >= tableSize && t.nextState[u] < 2 * tableSize);
    }
  }
});

test("streaming push/pop are exact inverses (symbol-level)", () => {
  const data = sampleSkewed(50_000, skewedProbabilities(24), 99);
  const norm = normalizeCounts(countFrequencies(data), 12);
  const tables = buildTables(norm, 12);

  const writer = new BitWriter();
  const enc = new TansEncoder(tables, writer);
  for (let i = data.length - 1; i >= 0; i--) enc.push(data[i]);
  enc.finish();

  const dec = new TansDecoder(tables, new ReverseBitReader(writer.finish()));
  const out = new Uint8Array(data.length);
  for (let i = 0; i < out.length; i++) out[i] = dec.pop();
  assert.deepEqual(out, data);
});

test("round-trip: edge cases", () => {
  roundTrip(new Uint8Array(0)); // empty
  roundTrip(new Uint8Array([42])); // single byte
  roundTrip(new Uint8Array(1000).fill(7)); // one distinct symbol
  roundTrip(new Uint8Array([0, 255, 0, 255, 0, 255])); // two symbols
  const all = new Uint8Array(256);
  for (let i = 0; i < 256; i++) all[i] = i; // full alphabet, uniform
  roundTrip(all);
});

test("round-trip: highly skewed sample", () => {
  const data = sampleSkewed(200_000, skewedProbabilities(32, 1.6), 1234);
  roundTrip(data);
});

test("round-trip: uniform random (incompressible) data", () => {
  const rand = mulberry32(5);
  const data = new Uint8Array(100_000);
  for (let i = 0; i < data.length; i++) data[i] = Math.floor(rand() * 256);
  const blob = roundTrip(data);
  // Must not expand beyond a small header + fractional-bit overhead.
  assert.ok(blob.length <= data.length * 1.01 + 64, `expanded to ${blob.length}`);
});

test("round-trip: text-like data", () => {
  const text =
    "the quick brown fox jumps over the lazy dog. ".repeat(4000) +
    "pack my box with five dozen liquor jugs! ".repeat(1000);
  roundTrip(new TextEncoder().encode(text));
});

test("round-trip: various table sizes", () => {
  const data = sampleSkewed(30_000, skewedProbabilities(16), 77);
  for (const tableLog of [8, 10, 12, 14, 15]) roundTrip(data, tableLog);
});

test("decompress rejects corrupt input", () => {
  assert.throws(() => decompress(new Uint8Array([1, 2, 3])), /magic/);
  const blob = compress(sampleSkewed(1000, skewedProbabilities(8), 3));
  const truncated = blob.subarray(0, blob.length - 2);
  assert.throws(() => decompress(truncated));
});
