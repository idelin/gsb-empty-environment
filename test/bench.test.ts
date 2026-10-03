import { test } from "node:test";
import assert from "node:assert/strict";
import { performance } from "node:perf_hooks";
import {
  compress,
  decompress,
  countFrequencies,
  normalizeCounts,
  arithmeticCompress,
  arithmeticDecompress,
} from "../src/index.ts";
import { sampleSkewed, skewedProbabilities } from "./util.ts";

function mbps(bytes: number, ms: number): number {
  return bytes / 1e6 / (ms / 1000);
}

test("throughput: tANS runs at a multiple of classic arithmetic coding", () => {
  const N = 2_000_000;
  const data = sampleSkewed(N, skewedProbabilities(48, 1.5), 31337);
  const tableLog = 12;
  const norm = normalizeCounts(countFrequencies(data), tableLog);

  let t0 = performance.now();
  const tansBlob = compress(data, tableLog);
  const tansEncMs = performance.now() - t0;

  t0 = performance.now();
  const tansBack = decompress(tansBlob);
  const tansDecMs = performance.now() - t0;
  assert.deepEqual(tansBack, data);

  t0 = performance.now();
  const arithBits = arithmeticCompress(data, norm, tableLog);
  const arithEncMs = performance.now() - t0;

  t0 = performance.now();
  const arithBack = arithmeticDecompress(arithBits, norm, tableLog, data.length);
  const arithDecMs = performance.now() - t0;
  assert.deepEqual(arithBack, data);

  const tansEnc = mbps(N, tansEncMs);
  const tansDec = mbps(N, tansDecMs);
  const arithEnc = mbps(N, arithEncMs);
  const arithDec = mbps(N, arithDecMs);

  console.log(`    tANS       : encode ${tansEnc.toFixed(1)} MB/s, decode ${tansDec.toFixed(1)} MB/s`);
  console.log(`    arithmetic : encode ${arithEnc.toFixed(1)} MB/s, decode ${arithDec.toFixed(1)} MB/s`);
  console.log(
    `    speedup    : encode ${(tansEnc / arithEnc).toFixed(1)}x, decode ${(tansDec / arithDec).toFixed(1)}x`,
  );

  // Acceptance: tANS decodes at pipeline speed — a multiple of the
  // bit-serial arithmetic decoder. Use a conservative 1.5x gate; in
  // practice the table-driven decoder is many times faster.
  assert.ok(
    tansDec > arithDec * 1.5,
    `tANS decode ${tansDec.toFixed(1)} MB/s vs arithmetic ${arithDec.toFixed(1)} MB/s`,
  );
  assert.ok(tansEnc > arithEnc, "tANS encode should also be faster");
});
