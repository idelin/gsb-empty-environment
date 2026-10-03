import { test } from "node:test";
import assert from "node:assert/strict";
import {
  compress,
  decompress,
  countFrequencies,
  normalizeCounts,
  shannonEntropyBits,
  shannonCodeLengthBits,
  arithmeticCompress,
  arithmeticDecompress,
} from "../src/index.ts";
import { sampleSkewed, skewedProbabilities, histogram } from "./util.ts";

// Highly skewed i.i.d. source: the acceptance scenario.
const N = 300_000;
const data = sampleSkewed(N, skewedProbabilities(40, 1.5), 2024);
const counts = histogram(data);
const entropyBits = shannonEntropyBits(counts); // theoretical optimum, total
const entropyPerSymbol = entropyBits / N;

test("compression ratio matches the theoretical entropy bound", () => {
  const blob = compress(data);
  assert.deepEqual(decompress(blob), data);

  const compressedBits = blob.length * 8;
  const overheadPerSymbol = compressedBits / N - entropyPerSymbol;
  console.log(
    `    entropy bound : ${entropyPerSymbol.toFixed(4)} bits/symbol (${Math.ceil(entropyBits / 8)} bytes)`,
  );
  console.log(
    `    tANS achieved : ${(compressedBits / N).toFixed(4)} bits/symbol (${blob.length} bytes, +${(overheadPerSymbol * 1000).toFixed(1)} mbit/symbol)`,
  );

  // Within 1% of the optimal bound plus a small absolute slack (header+state).
  assert.ok(
    compressedBits <= entropyBits * 1.01 + 2048,
    `compressed ${compressedBits} bits vs bound ${entropyBits} bits`,
  );
  // And certainly not below the bound (sanity: no cheating).
  assert.ok(compressedBits > entropyBits * 0.98);
});

test("beats integer-length (Huffman/Shannon-style) codes on skewed input", () => {
  const blob = compress(data);
  const shannonBits = shannonCodeLengthBits(counts);
  console.log(
    `    Huffman-class : ${(shannonBits / N).toFixed(4)} bits/symbol vs tANS ${((blob.length * 8) / N).toFixed(4)}`,
  );
  assert.ok(blob.length * 8 < shannonBits, "tANS should beat ceil-length codes");
});

test("ratio parity with a classic arithmetic coder", () => {
  const norm = normalizeCounts(countFrequencies(data), 12);
  const tableLog = 12;

  const tansBlob = compress(data, tableLog);
  const arithBits = arithmeticCompress(data, norm, tableLog);
  const arithBack = arithmeticDecompress(arithBits, norm, tableLog, data.length);
  assert.deepEqual(arithBack, data, "arithmetic coder round-trip");

  console.log(
    `    tANS payload  : ${tansBlob.length} bytes | arithmetic payload: ${arithBits.length} bytes`,
  );
  // tANS payload (incl. header) must be within 0.5% + 64 bytes of arithmetic coding.
  assert.ok(tansBlob.length <= arithBits.length * 1.005 + 64);
});
