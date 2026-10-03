/**
 * Throughput benchmark: tANS vs classic 32-bit arithmetic coder,
 * identical probability model (same normalised frequencies).
 * Best-of-N rounds after a JIT warmup pass.
 */
import { BitReader, BitWriter } from "../src/bitstream.ts";
import { buildTable, countSymbols, normalizeFrequencies } from "../src/table.ts";
import { decodeAll, encodeAll } from "../src/tans.ts";
import { ArithmeticDecoder, ArithmeticEncoder, cumulative } from "../src/arithmetic.ts";

function skewedSample(n: number, alphabet: number, skew: number, seed = 42): Uint8Array {
  let s = seed >>> 0;
  const rng = () => ((s = (s * 1664525 + 1013904223) >>> 0) / 0x100000000);
  const weights = Array.from({ length: alphabet }, (_, i) => Math.pow(skew, i));
  const total = weights.reduce((a, b) => a + b, 0);
  const cum = weights.map((_, i) => weights.slice(0, i + 1).reduce((a, b) => a + b, 0) / total);
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) {
    const r = rng();
    let k = 0;
    while (k < alphabet - 1 && r > cum[k]) k++;
    out[i] = k;
  }
  return out;
}

const N = 2_000_000;
const LOG = 12;
const ROUNDS = 5;
const data = skewedSample(N, 32, 0.6);
const freq = normalizeFrequencies(countSymbols(data), LOG);
const table = buildTable(freq, LOG);
const cum = cumulative(freq);
const total = 1 << LOG;

function tansEncode() {
  return encodeAll(table, data);
}
function tansDecode(payload: { bytes: Uint8Array; bitLength: number }) {
  const out = decodeAll(table, payload.bytes, payload.bitLength, N);
  if (out[0] !== data[0]) throw new Error("tANS roundtrip failed");
}
function arithEncode() {
  const w = new BitWriter();
  const enc = new ArithmeticEncoder(cum, total, w);
  for (const b of data) enc.encode(b);
  enc.finish();
  return w.finish();
}
function arithDecode(payload: { bytes: Uint8Array; bitLength: number }) {
  const dec = new ArithmeticDecoder(cum, total, new BitReader(payload.bytes, payload.bitLength), 256);
  let checksum = 0;
  for (let i = 0; i < N; i++) checksum ^= dec.decode();
  let expected = 0;
  for (const b of data) expected ^= b;
  if (checksum !== expected) throw new Error("arithmetic roundtrip failed");
}

function bestOf(fn: () => void): number {
  let best = Infinity;
  for (let i = 0; i < ROUNDS; i++) {
    const t0 = performance.now();
    fn();
    best = Math.min(best, performance.now() - t0);
  }
  return best;
}

// warmup (JIT) + correctness
const tansPayload = tansEncode();
const arithPayload = arithEncode();
tansDecode(tansPayload);
arithDecode(arithPayload);
const fullTans = decodeAll(table, tansPayload.bytes, tansPayload.bitLength, N);
if (Buffer.compare(Buffer.from(fullTans), Buffer.from(data)) !== 0) throw new Error("tANS mismatch");

const mb = N / 1e6;
const tansEnc = mb / (bestOf(() => tansEncode()) / 1000);
const tansDec = mb / (bestOf(() => tansDecode(tansPayload)) / 1000);
const arithEnc = mb / (bestOf(() => arithEncode()) / 1000);
const arithDec = mb / (bestOf(() => arithDecode(arithPayload)) / 1000);

console.log(`sample: ${N.toLocaleString()} bytes, skewed 32-symbol alphabet, tableLog=${LOG}, best of ${ROUNDS}`);
console.log("");
console.log("            encode MB/s   decode MB/s   output bytes");
console.log(`tANS        ${tansEnc.toFixed(1).padStart(10)}   ${tansDec.toFixed(1).padStart(11)}   ${tansPayload.bytes.length.toLocaleString().padStart(12)}`);
console.log(`arithmetic  ${arithEnc.toFixed(1).padStart(10)}   ${arithDec.toFixed(1).padStart(11)}   ${arithPayload.bytes.length.toLocaleString().padStart(12)}`);
console.log("");
console.log(`speedup:    encode ${(tansEnc / arithEnc).toFixed(1)}x   decode ${(tansDec / arithDec).toFixed(1)}x`);
console.log(`size delta: ${(((tansPayload.bitLength - arithPayload.bitLength) / arithPayload.bitLength) * 100).toFixed(3)}% vs arithmetic coder`);
