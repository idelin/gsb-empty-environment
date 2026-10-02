import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  SparseVector,
  packDeltas,
  packedSize,
  rleCompress,
  rleDecompress,
  unpackDeltas,
} from "../src/sparse.ts";
import { TruncationError } from "../src/errors.ts";

describe("SparseVector adaptive container", () => {
  it("starts sparse and stays sparse for scattered writes", () => {
    const v = new SparseVector();
    for (let i = 0; i < 1_000_000; i += 10_000) v.set(i, i + 1);
    assert.equal(v.mode, "sparse");
    assert.equal(v.size, 100);
    const s = v.stats();
    assert.ok(s.currentBytes < s.denseBytes, "sparse must be cheaper here");
  });

  it("converts to dense once density crosses the threshold", () => {
    const v = new SparseVector({ denseThreshold: 0.4 });
    for (let i = 0; i < 1000; i++) v.set(i, i + 1);
    assert.equal(v.mode, "dense");
    for (let i = 0; i < 1000; i++) assert.equal(v.get(i), i + 1);
  });

  it("converts back to sparse when density collapses", () => {
    const v = new SparseVector({ denseThreshold: 0.4 });
    for (let i = 0; i < 1000; i++) v.set(i, i + 1);
    assert.equal(v.mode, "dense");
    for (let i = 0; i < 990; i++) v.delete(i);
    assert.equal(v.mode, "sparse");
    assert.equal(v.size, 10);
    for (let i = 990; i < 1000; i++) assert.equal(v.get(i), i + 1);
  });

  it("preserves all values across both conversion directions", () => {
    const v = new SparseVector({ denseThreshold: 0.3 });
    const ref = new Map<number, number>();
    let seed = 42;
    const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff);
    for (let round = 0; round < 20; round++) {
      for (let k = 0; k < 200; k++) {
        const i = rand() % 500;
        const value = rand() % 7 === 0 ? 0 : rand() % 1000;
        v.set(i, value);
        if (value === 0) ref.delete(i);
        else ref.set(i, value);
      }
    }
    assert.equal(v.size, ref.size);
    for (const [i, value] of ref) assert.equal(v.get(i), value);
  });

  it("treats fill-value writes as deletions", () => {
    const v = new SparseVector({ fill: 0 });
    v.set(5, 10);
    v.set(5, 0);
    assert.equal(v.size, 0);
    assert.equal(v.get(5), 0);
  });

  it("supports non-zero fill values", () => {
    const v = new SparseVector({ fill: -1 });
    v.set(3, 5);
    assert.equal(v.get(0), -1);
    assert.equal(v.get(3), 5);
    for (let i = 0; i < 100; i++) v.set(i, i + 1); // force dense
    assert.equal(v.mode, "dense");
    v.set(3, -1);
    assert.equal(v.get(3), -1);
    assert.equal(v.get(50), 51);
  });

  it("validates indices", () => {
    const v = new SparseVector();
    assert.throws(() => v.set(-1, 1), RangeError);
    assert.throws(() => v.set(1.5, 1), RangeError);
    assert.throws(() => v.get(10), RangeError); // beyond logical length
  });

  it("iterates entries in ascending index order", () => {
    const v = new SparseVector();
    v.set(9, 90);
    v.set(1, 10);
    v.set(5, 50);
    assert.deepEqual([...v.entries()], [[1, 10], [5, 50], [9, 90]]);
  });

  it("clear() releases all storage (no leak)", () => {
    const v = new SparseVector();
    for (let i = 0; i < 10_000; i++) v.set(i, i + 1);
    const before = v.byteSize();
    assert.ok(before > 0);
    v.clear();
    assert.equal(v.length, 0);
    assert.equal(v.size, 0);
    assert.equal(v.mode, "sparse");
    assert.equal(v.byteSize(), 64, "storage must return to the empty baseline");
  });
});

describe("RLE compression", () => {
  it("round-trips run-dominated data", () => {
    const data = [...new Array(50_000).fill(0), 1, 2, 2, 2, ...new Array(10_000).fill(7)];
    const block = rleCompress(data);
    assert.ok(block.pairs.length / 2 <= 5, "should collapse to a handful of runs");
    assert.deepEqual(rleDecompress(block), data);
  });

  it("handles empty and single-element input", () => {
    assert.deepEqual(rleDecompress(rleCompress([])), []);
    assert.deepEqual(rleDecompress(rleCompress([9])), [9]);
  });

  it("rejects corrupt blocks", () => {
    assert.throws(() => rleDecompress({ pairs: [1], length: 1 }), /odd pair count/);
    assert.throws(() => rleDecompress({ pairs: [1, -2], length: 1 }), /bad run length/);
    assert.throws(() => rleDecompress({ pairs: [1, 2], length: 5 }), /expected 5 items/);
  });
});

describe("delta + zigzag + varint packing", () => {
  it("round-trips sorted sparse indices", () => {
    const indices: number[] = [];
    for (let i = 0; i < 100_000; i += 97) indices.push(i);
    const packed = packDeltas(indices);
    assert.ok(
      packed.length <= indices.length * 3,
      `gaps of 97 pack to ~2 bytes vs 8 raw (got ${packed.length} for ${indices.length})`,
    );
    assert.deepEqual(unpackDeltas(packed), indices.map(BigInt));
    assert.equal(packed.length, packedSize(indices));
  });

  it("round-trips non-monotonic sequences via zigzag deltas", () => {
    const values = [0n, -5n, 100n, -100n, (1n << 62n), -(1n << 62n), 7n];
    assert.deepEqual(unpackDeltas(packDeltas(values)), values);
  });

  it("handles empty input", () => {
    assert.deepEqual(unpackDeltas(packDeltas([])), []);
  });

  it("rejects truncated and trailing-garbage packs", () => {
    const packed = packDeltas([1, 2, 3, 4]);
    assert.throws(() => unpackDeltas(packed.subarray(0, packed.length - 1)), TruncationError);
    const padded = new Uint8Array([...packed, 0x00]);
    assert.throws(() => unpackDeltas(padded), /trailing byte/);
  });
});
