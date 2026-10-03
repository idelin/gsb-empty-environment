/**
 * Demo: compress a highly skewed sample with tANS and show how close the
 * output sits to the Shannon entropy bound.
 */
import { compress, decompress, shannonEntropy } from "../src/codec.ts";

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

const N = 1_000_000;
const data = skewedSample(N, 32, 0.6);
console.log(`input:            ${N.toLocaleString()} bytes (skewed, 32-symbol alphabet)`);
console.log(`shannon entropy:  ${shannonEntropy(data).toFixed(4)} bits/symbol`);

const t0 = performance.now();
const c = compress(data, 12);
const t1 = performance.now();
const d = decompress(c.bytes);
const t2 = performance.now();

console.log(`compressed:       ${c.compressedBytes.toLocaleString()} bytes  (ratio ${c.ratio.toFixed(2)}x)`);
console.log(`achieved:         ${c.achievedBitsPerSymbol.toFixed(4)} bits/symbol (incl. header)`);
console.log(`gap to entropy:   ${((c.achievedBitsPerSymbol / c.entropyBitsPerSymbol - 1) * 100).toFixed(2)}%`);
console.log(`roundtrip ok:     ${Buffer.compare(Buffer.from(d), Buffer.from(data)) === 0}`);
console.log(`encode throughput:${(N / (t1 - t0) / 1000).toFixed(1)} MB/s`);
console.log(`decode throughput:${(N / (t2 - t1) / 1000).toFixed(1)} MB/s`);
