import { test } from "node:test";
import assert from "node:assert/strict";
import {
  BigIntAnsEncoder,
  BigIntAnsDecoder,
  buildModel,
  encodeToBigInt,
  decodeFromBigInt,
  countFrequencies,
  normalizeCounts,
  shannonEntropyBits,
} from "../src/index.ts";
import { sampleSkewed, skewedProbabilities, histogram } from "./util.ts";

test("the entire message folds into a single big-integer state", () => {
  const data = new TextEncoder().encode("asymmetric numeral systems ".repeat(50));
  const { state, model } = encodeToBigInt(data, 12);

  assert.equal(typeof state, "bigint");
  assert.ok(state > 0n);

  const back = decodeFromBigInt(state, model, data.length);
  assert.deepEqual(back, data);
});

test("push/pop on the big-integer state are exact inverses", () => {
  const data = sampleSkewed(3000, skewedProbabilities(12), 11);
  const norm = normalizeCounts(countFrequencies(data), 10);
  const model = buildModel(norm, 10);

  const enc = new BigIntAnsEncoder(model);
  const states: bigint[] = [];
  for (let i = data.length - 1; i >= 0; i--) {
    enc.push(data[i]);
    states.push(enc.state);
  }
  // State strictly grows on a skewed stream (information accumulates).
  assert.ok(enc.state > model.modulus);

  const dec = new BigIntAnsDecoder(enc.state, model);
  const out = new Uint8Array(data.length);
  for (let i = 0; i < out.length; i++) out[i] = dec.pop();
  assert.deepEqual(out, data);
  // Fully unwound back to the initial state.
  assert.equal(dec.state, model.modulus);
});

test("big-integer state size tracks the entropy bound", () => {
  const data = sampleSkewed(20_000, skewedProbabilities(24, 1.5), 21);
  const { state } = encodeToBigInt(data, 12);
  const stateBits = state.toString(2).length;
  const entropyBits = shannonEntropyBits(histogram(data));
  console.log(
    `    single-integer state: ${stateBits} bits vs entropy ${entropyBits.toFixed(0)} bits`,
  );
  assert.ok(stateBits >= entropyBits - 16);
  assert.ok(stateBits <= entropyBits + 64);
});
