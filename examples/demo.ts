// Demo: compress a synthetic system log, then decompress and verify
// byte-exact integrity via SHA256.
import { createHash } from 'node:crypto';
import { deflate, inflate } from '../src/index.ts';
import { generateSystemLog } from './syslog.ts';

const sha256 = (b: Buffer) => createHash('sha256').update(b).digest('hex');

const original = generateSystemLog(1024 * 1024);
console.log(`original:    ${original.length.toLocaleString()} bytes`);
console.log(`sha256:      ${sha256(original)}`);

const t0 = performance.now();
const compressed = deflate(original);
const t1 = performance.now();
console.log(`compressed:  ${compressed.length.toLocaleString()} bytes ` +
  `(${(compressed.length / original.length * 100).toFixed(1)}% of original) in ${(t1 - t0).toFixed(0)} ms`);
console.log(`block type:  ${['stored', 'fixed-huffman', 'dynamic-huffman'][(compressed[0] >> 1) & 3]}`);

const t2 = performance.now();
const restored = inflate(compressed);
const t3 = performance.now();
console.log(`restored:    ${restored.length.toLocaleString()} bytes in ${(t3 - t2).toFixed(0)} ms`);
console.log(`sha256:      ${sha256(restored)}`);
console.log(sha256(restored) === sha256(original)
  ? 'OK: SHA256 identical, byte-exact round trip'
  : 'FAIL: hash mismatch');
