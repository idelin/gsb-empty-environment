/**
 * End-to-end demo: generate a large whitespace-dominated technical
 * drawing, compress it with the RLE+Huffman codec, measure speed and
 * ratio, decode it back and verify pixel-exact reconstruction.
 *
 * Run with:  node examples/demo.ts        (Node >= 22.18 / 23.6)
 */

import { compress, decompress, rawSizeBytes, type RasterImage } from '../src/index.ts';
import { inkCoverage, makeTechnicalDrawing } from '../src/drawing.ts';

function asciiPreview(image: RasterImage, cols: number, rows: number): string {
  const { width, height, pixels } = image;
  const lines: string[] = [];
  for (let r = 0; r < rows; r++) {
    let line = '';
    for (let c = 0; c < cols; c++) {
      const x0 = Math.floor((c * width) / cols);
      const x1 = Math.floor(((c + 1) * width) / cols);
      const y0 = Math.floor((r * height) / rows);
      const y1 = Math.floor(((r + 1) * height) / rows);
      let ink = 0;
      for (let y = y0; y < y1; y++) {
        for (let x = x0; x < x1; x++) ink += pixels[y * width + x];
      }
      const density = ink / ((x1 - x0) * (y1 - y0));
      line += density > 0.25 ? '#' : density > 0.05 ? '+' : density > 0 ? '.' : ' ';
    }
    lines.push(line);
  }
  return lines.join('\n');
}

function pixelsEqual(a: RasterImage, b: RasterImage): boolean {
  if (a.width !== b.width || a.height !== b.height || a.bitDepth !== b.bitDepth) return false;
  if (a.pixels.length !== b.pixels.length) return false;
  for (let i = 0; i < a.pixels.length; i++) if (a.pixels[i] !== b.pixels[i]) return false;
  return true;
}

// ---------------------------------------------------------------------

const WIDTH = 2048;
const HEIGHT = 2048;

console.log(`Generating ${WIDTH}x${HEIGHT} binary technical drawing...`);
const drawing = makeTechnicalDrawing(WIDTH, HEIGHT);
console.log(`Ink coverage: ${(inkCoverage(drawing) * 100).toFixed(2)}% black pixels`);

const rawBytes = rawSizeBytes(drawing);

// Warm up JIT + preset tables, then take the best of several runs.
compress(drawing);
let encodeMs = Infinity;
let decodeMs = Infinity;
let compressed: Uint8Array = new Uint8Array(0);
let restored = drawing;
for (let attempt = 0; attempt < 5; attempt++) {
  const t0 = performance.now();
  compressed = compress(drawing);
  const t1 = performance.now();
  restored = decompress(compressed);
  const t2 = performance.now();
  encodeMs = Math.min(encodeMs, t1 - t0);
  decodeMs = Math.min(decodeMs, t2 - t1);
}

const ratio = compressed.length / rawBytes;
const ok = pixelsEqual(drawing, restored);

console.log('');
console.log('=== RLE + Huffman composite codec ===');
console.log(`Raw raster size   : ${rawBytes.toLocaleString()} bytes (1 bpp packed)`);
console.log(`Compressed size   : ${compressed.length.toLocaleString()} bytes`);
console.log(`Compression ratio : ${(ratio * 100).toFixed(2)}% of original (${(1 / ratio).toFixed(1)}x)`);
console.log(`Encode time       : ${encodeMs.toFixed(2)} ms (best of 5)`);
console.log(`Decode time       : ${decodeMs.toFixed(2)} ms (best of 5)`);
console.log(`Round-trip exact  : ${ok ? 'YES - pixel matrix strictly identical' : 'NO - MISMATCH!'}`);
console.log(`Acceptance (<5%)  : ${ratio < 0.05 ? 'PASS' : 'FAIL'}`);
console.log('');
console.log('Preview (downsampled):');
console.log(asciiPreview(restored, 96, 40));

if (!ok) process.exitCode = 1;
