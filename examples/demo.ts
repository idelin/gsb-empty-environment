import { performance } from 'node:perf_hooks';
import { compress, decompress } from '../src/compressor.ts';
import { bitmapsEqual } from '../src/bitmap.ts';
import { generateTechnicalDrawing } from './drawing.ts';

function formatBytes(bytes: number): string {
  if (bytes >= 1 << 20) return `${(bytes / (1 << 20)).toFixed(2)} MiB`;
  if (bytes >= 1 << 10) return `${(bytes / (1 << 10)).toFixed(2)} KiB`;
  return `${bytes} B`;
}

const drawing = generateTechnicalDrawing();
const { width, height } = drawing;
const raw8bpp = width * height;
const raw1bpp = Math.ceil(raw8bpp / 8);

const t0 = performance.now();
const compressed = compress(drawing);
const t1 = performance.now();
const decoded = decompress(compressed);
const t2 = performance.now();

const identical = bitmapsEqual(drawing, decoded);
const ratioPacked = compressed.length / raw1bpp;
const ratioRaw = compressed.length / raw8bpp;

console.log('=== RLE + preset-Huffman compound raster compressor ===');
console.log(`input            : technical drawing ${width} x ${height} (binary, 1 bit/px)`);
console.log(`raw (8 bit/px)   : ${formatBytes(raw8bpp)}`);
console.log(`raw (1 bit/px)   : ${formatBytes(raw1bpp)}`);
console.log(`compressed       : ${formatBytes(compressed.length)}`);
console.log(`ratio vs 1 bit/px: ${(ratioPacked * 100).toFixed(3)}%  (target < 5%)`);
console.log(`ratio vs 8 bit/px: ${(ratioRaw * 100).toFixed(3)}%`);
console.log(`compress time    : ${(t1 - t0).toFixed(2)} ms`);
console.log(`decompress time  : ${(t2 - t1).toFixed(2)} ms`);
console.log(`round-trip exact : ${identical ? 'YES (pixel-perfect, aligned)' : 'NO'}`);

if (!identical) {
  console.error('FAIL: decoded raster does not match the source bitmap');
  process.exit(1);
}
if (ratioPacked >= 0.05) {
  console.error('FAIL: compressed size exceeds 5% of the 1 bit/px original');
  process.exit(1);
}
console.log('OK: all acceptance criteria met.');
