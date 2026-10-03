/**
 * End-to-end demo: highly skewed sample -> tANS vs arithmetic coding vs
 * theoretical bounds, plus the single-big-integer ANS variant.
 *
 * Run:  node examples/demo.ts        (Node >= 23.6, type stripping built in)
 */
import { performance } from "node:perf_hooks";
import {
  compress,
  decompress,
  countFrequencies,
  normalizeCounts,
  shannonEntropyBits,
  shannonCodeLengthBits,
  arithmeticCompress,
  arithmeticDecompress,
  encodeToBigInt,
  decodeFromBigInt,
} from "../src/index.ts";

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// --- Highly skewed test sample: Zipf-like distribution over 48 byte symbols --
const N = 4_000_000;
const SYMBOLS = 48;
const cdf: number[] = [];
let acc = 0;
for (let i = 0; i < SYMBOLS; i++) {
  acc += 1 / Math.pow(i + 1, 1.5);
  cdf.push(acc);
}
for (let i = 0; i < SYMBOLS; i++) cdf[i] /= acc;

const rand = mulberry32(20261004);
const data = new Uint8Array(N);
for (let i = 0; i < N; i++) {
  const r = rand();
  let lo = 0;
  let hi = SYMBOLS - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (r <= cdf[mid]) hi = mid;
    else lo = mid + 1;
  }
  data[i] = lo;
}

const mb = (n: number) => (n / 1e6).toFixed(2) + " MB";
const mbps = (bytes: number, ms: number) => (bytes / 1e6 / (ms / 1000)).toFixed(1) + " MB/s";

console.log("=== tANS finite-state entropy codec — demo ===\n");
console.log(`sample            : ${mb(N)} highly skewed i.i.d. (Zipf, ${SYMBOLS} symbols)`);

const counts = countFrequencies(data);
const entropyBits = shannonEntropyBits(counts);
const huffmanBits = shannonCodeLengthBits(counts);
console.log(`entropy bound     : ${(entropyBits / N).toFixed(4)} bits/symbol -> ${mb(entropyBits / 8)} (optimal)`);
console.log(`Huffman-class     : ${(huffmanBits / N).toFixed(4)} bits/symbol -> ${mb(huffmanBits / 8)} (integer code lengths)`);
console.log();

// --- tANS -------------------------------------------------------------------
const TABLE_LOG = 12;
let t0 = performance.now();
const tansBlob = compress(data, TABLE_LOG);
const encMs = performance.now() - t0;
t0 = performance.now();
const tansBack = decompress(tansBlob);
const decMs = performance.now() - t0;
console.log(`tANS (tableLog=${TABLE_LOG})  : ${mb(tansBlob.length)} = ${((tansBlob.length * 8) / N).toFixed(4)} bits/symbol`);
console.log(`  round-trip OK   : ${Buffer.from(tansBack).equals(Buffer.from(data))}`);
console.log(`  vs entropy bound: +${(((tansBlob.length * 8) / entropyBits - 1) * 100).toFixed(3)}% (incl. header)`);
console.log(`  throughput      : encode ${mbps(N, encMs)}, decode ${mbps(N, decMs)}`);
console.log();

// --- Arithmetic coder baseline ----------------------------------------------
const norm = normalizeCounts(counts, TABLE_LOG);
t0 = performance.now();
const arithBits = arithmeticCompress(data, norm, TABLE_LOG);
const aEncMs = performance.now() - t0;
t0 = performance.now();
const arithBack = arithmeticDecompress(arithBits, norm, TABLE_LOG, N);
const aDecMs = performance.now() - t0;
console.log(`arithmetic coder: ${mb(arithBits.length)} payload (same static model, no header)`);
console.log(`  round-trip OK   : ${Buffer.from(arithBack).equals(Buffer.from(data))}`);
console.log(`  throughput      : encode ${mbps(N, aEncMs)}, decode ${mbps(N, aDecMs)}`);
console.log(
  `  tANS speedup    : encode ${(aEncMs / encMs).toFixed(1)}x, decode ${(aDecMs / decMs).toFixed(1)}x`,
);
console.log();

// --- Single-big-integer ANS ---------------------------------------------------
const msg = new TextEncoder().encode("asymmetric numeral systems: the whole message in one integer");
const { state, model } = encodeToBigInt(msg, 12);
const restored = decodeFromBigInt(state, model, msg.length);
console.log(`single-big-integer ANS:`);
console.log(`  message         : "${new TextDecoder().decode(msg)}"`);
console.log(`  one BigInt state: ${state.toString(2).length} bits (${state.toString().slice(0, 40)}...)`);
console.log(`  decoded OK      : ${new TextDecoder().decode(restored) === new TextDecoder().decode(msg)}`);
