import { encode, decode, binaryEntropy } from './arithmetic.ts';

// Deterministic PRNG (mulberry32) so the demo is reproducible.
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const N = 1_000_000;
const P_ZERO = 0.9;
const rand = mulberry32(42);
const symbols = new Uint8Array(N);
let zeros = 0;
for (let i = 0; i < N; i++) {
  symbols[i] = rand() < P_ZERO ? 0 : 1;
  if (symbols[i] === 0) zeros++;
}

const compressed = encode(symbols);
const restored = decode(compressed);

let mismatch = 0;
for (let i = 0; i < N; i++) if (restored[i] !== symbols[i]) mismatch++;

const entropy = binaryEntropy(zeros / N); // bits/symbol of the actual source
const entropyBytes = (entropy * N) / 8;
const huffmanBoundBytes = Math.ceil(N / 8); // binary Huffman: >= 1 bit/symbol

console.log(`symbols:            ${N.toLocaleString()}  (P(0) = ${(zeros / N).toFixed(4)})`);
console.log(`Shannon entropy:    ${entropy.toFixed(4)} bits/symbol  ->  ${Math.ceil(entropyBytes).toLocaleString()} bytes (theoretical limit)`);
console.log(`Huffman lower bound: 1.0000 bits/symbol  ->  ${huffmanBoundBytes.toLocaleString()} bytes (binary alphabet)`);
console.log(`arithmetic coded:   ${((compressed.length * 8) / N).toFixed(4)} bits/symbol  ->  ${compressed.length.toLocaleString()} bytes`);
console.log(`vs Huffman bound:   ${((1 - compressed.length / huffmanBoundBytes) * 100).toFixed(1)}% smaller`);
console.log(`round-trip:         ${mismatch === 0 && restored.length === N ? 'LOSSLESS, exact termination' : `FAILED (${mismatch} mismatches)`}`);
