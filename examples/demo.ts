// Demo: compress a binary stream with P(0) = 90% and compare against the
// Shannon entropy limit and the Huffman theoretical lower bound.

import { strict as assert } from "node:assert";
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

function h2(p: number): number {
  return -p * Math.log2(p) - (1 - p) * Math.log2(1 - p);
}

const N: number = 1000000;
const P0: number = 0.9;

const rand: RandFn = xorshift32(20261002);
const data: Uint8Array = new Uint8Array(N);
let zeros: number = 0;
for (let i: number = 0; i < N; i++) {
  const b: number = rand() / 4294967296 < P0 ? 0 : 1;
  data[i] = b;
  if (b === 0) {
    zeros += 1;
  }
}

const model: FrequencyModel = new FrequencyModel([0, 1], [9, 1]);

const t0: number = Date.now();
const packed: Uint8Array = compress(data, model);
const t1: number = Date.now();
const back: Uint8Array = decompress(packed);
const t2: number = Date.now();

assert.deepEqual(back, data);

const entropyBits: number = N * h2(P0);
const huffmanBytes: number = Math.ceil(N / 8);
const entropyBytes: number = entropyBits / 8;

console.log("=== Arithmetic coding demo (BigInt rational interval, " + PRECISION + "-bit state) ===");
console.log("symbols              : " + N + "  (actual P(0) = " + (zeros / N).toFixed(4) + ")");
console.log("model                : P(0) = 9/10, P(1) = 1/10 (static, sent in header)");
console.log("raw size             : " + N + " bytes (1 byte/symbol)");
console.log("Huffman lower bound  : " + huffmanBytes + " bytes (1 bit/symbol)");
console.log("Shannon entropy limit: " + entropyBytes.toFixed(1) + " bytes (" + h2(P0).toFixed(4) + " bits/symbol)");
console.log("arithmetic coded     : " + packed.length + " bytes (header included)");
console.log("  vs Huffman bound   : " + (100 * (1 - packed.length / huffmanBytes)).toFixed(1) + "% smaller");
console.log("  vs entropy limit   : +" + (100 * (packed.length / entropyBytes - 1)).toFixed(3) + "% overhead");
console.log("roundtrip            : OK (" + N + " symbols decoded exactly)");
console.log("encode " + (t1 - t0) + " ms, decode " + (t2 - t1) + " ms");
