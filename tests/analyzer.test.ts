import { test } from "node:test";
import assert from "node:assert/strict";
import {
  analyzeBitWidths,
  analyzeLoad,
  hashSamples,
  verdictForCv,
} from "../src/analyzer.ts";
import { fnv1a_32, md5_32, murmur3_32 } from "../src/hashes.ts";
import { mulberry32, sequentialUserKeys } from "../src/sampler.ts";

const SAMPLES = 100_000;
const NODES = Array.from({ length: 16 }, (_, i) => `node-${i}`);
const VNODES = 100;
const WIDTHS = [4, 6, 8, 10, 12];

function sequentialHashes(hash: typeof md5_32): Uint32Array {
  return hashSamples(hash, SAMPLES, sequentialUserKeys);
}

test("verdictForCv grades against the theoretical ring floor", () => {
  const theory = 0.1;
  assert.equal(verdictForCv(0.1, theory), "PASS");
  assert.equal(verdictForCv(0.18, theory), "WARN");
  assert.equal(verdictForCv(0.4, theory), "FAIL");
});

test("md5 and murmur3 reach near-ideal ring load balance", () => {
  for (const [name, hash] of [
    ["md5", md5_32],
    ["murmur3", murmur3_32],
  ] as const) {
    const report = analyzeLoad({
      hashName: name,
      hash,
      nodes: NODES,
      sampleCount: SAMPLES,
      vnodesPerNode: VNODES,
      nextKey: sequentialUserKeys,
    });
    assert.equal(report.verdict, "PASS", `${name} cvRatio=${report.cvRatio}`);
    assert.ok(report.cvRatio < 1.5);
    assert.ok(report.maxMinRatio < 2);
    assert.equal(report.ringCollisions, 0);
  }
});

test("fnv1a fails ring load balance even on random keys", () => {
  const rand = mulberry32(99);
  const report = analyzeLoad({
    hashName: "fnv1a",
    hash: fnv1a_32,
    nodes: NODES,
    sampleCount: SAMPLES,
    vnodesPerNode: VNODES,
    nextKey: () => `user:${Math.floor(rand() * 1e9)}`,
  });
  // Ring point keys ("node-i#v") are structured; FNV's weak avalanche
  // misplaces them, so load CV sits far above the theoretical floor.
  assert.equal(report.verdict, "FAIL");
  assert.ok(report.cvRatio > 2.5);
});

test("bit-width curve pinpoints fnv1a avalanche deficiency", () => {
  const hashes = sequentialHashes(fnv1a_32);
  const points = analyzeBitWidths(hashes, WIDTHS);
  const deficient = points.filter((p) => p.deficient).map((p) => p.bits);
  assert.ok(
    deficient.includes(8),
    `expected 8-bit deficiency, got [${deficient}]`,
  );
  const at8 = points.find((p) => p.bits === 8)!;
  assert.ok(at8.normalized > 2, `chi2/df at 8 bits = ${at8.normalized}`);
  assert.ok(at8.pValue < 1e-3);
});

test("md5 and murmur3 stay uniform at every tested bit width", () => {
  for (const hash of [md5_32, murmur3_32]) {
    const points = analyzeBitWidths(sequentialHashes(hash), WIDTHS);
    const bad = points.filter((p) => p.deficient);
    assert.equal(bad.length, 0, `unexpected deficiency: ${JSON.stringify(bad)}`);
  }
});

test("uniform random hash values pass the bit-width curve", () => {
  const rand = mulberry32(1234);
  const hashes = new Uint32Array(SAMPLES);
  for (let i = 0; i < SAMPLES; i++) hashes[i] = Math.floor(rand() * 0x100000000);
  const points = analyzeBitWidths(hashes, WIDTHS);
  assert.ok(points.every((p) => !p.deficient));
});

test("analyzeBitWidths rejects invalid widths", () => {
  assert.throws(() => analyzeBitWidths(new Uint32Array(8), [0]));
  assert.throws(() => analyzeBitWidths(new Uint32Array(8), [25]));
});
