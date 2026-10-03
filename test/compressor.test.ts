import { test } from 'node:test';
import assert from 'node:assert/strict';
import { performance } from 'node:perf_hooks';
import { compress, decompress } from '../src/compressor.ts';
import { bitmapsEqual, createBitmap, type BinaryBitmap } from '../src/bitmap.ts';
import { generateTechnicalDrawing, mulberry32 } from '../examples/drawing.ts';

function roundTrip(bitmap: BinaryBitmap, label: string): void {
  const compressed = compress(bitmap);
  const decoded = decompress(compressed);
  assert.equal(decoded.width, bitmap.width, `${label}: width`);
  assert.equal(decoded.height, bitmap.height, `${label}: height`);
  assert.ok(bitmapsEqual(bitmap, decoded), `${label}: pixels differ after round-trip`);
}

test('round-trips a blank (all-white) bitmap', () => {
  roundTrip(createBitmap(640, 480), 'blank');
});

test('round-trips a fully black bitmap', () => {
  roundTrip(createBitmap(317, 211, 1), 'black');
});

test('round-trips a checkerboard (worst case for RLE)', () => {
  const bmp = createBitmap(101, 53);
  for (let y = 0; y < bmp.height; y++) {
    for (let x = 0; x < bmp.width; x++) {
      bmp.pixels[y * bmp.width + x] = (x + y) & 1;
    }
  }
  roundTrip(bmp, 'checkerboard');
});

test('round-trips vertical stripes', () => {
  const bmp = createBitmap(512, 128);
  for (let y = 0; y < bmp.height; y++) {
    for (let x = 0; x < bmp.width; x++) {
      bmp.pixels[y * bmp.width + x] = x % 7 < 2 ? 1 : 0;
    }
  }
  roundTrip(bmp, 'stripes');
});

test('round-trips random sparse noise', () => {
  const rng = mulberry32(7);
  const bmp = createBitmap(777, 333);
  for (let i = 0; i < bmp.pixels.length; i++) {
    bmp.pixels[i] = rng() < 0.02 ? 1 : 0;
  }
  roundTrip(bmp, 'sparse noise');
});

test('round-trips tiny and odd-sized bitmaps', () => {
  roundTrip(createBitmap(1, 1), '1x1');
  roundTrip(createBitmap(1, 1, 1), '1x1 black');
  roundTrip(createBitmap(7, 3), '7x3');
  roundTrip(createBitmap(0, 0), 'empty');
});

test('round-trips rows wider than the max make-up code (escape path)', () => {
  const bmp = createBitmap(9000, 4);
  bmp.pixels[2 * 9000 + 4500] = 1;
  roundTrip(bmp, 'escape path');
});

test('round-trips the sample technical drawing with strict pixel alignment', () => {
  const drawing = generateTechnicalDrawing();
  const decoded = decompress(compress(drawing));
  assert.equal(decoded.width, drawing.width);
  assert.equal(decoded.height, drawing.height);
  for (let y = 0; y < drawing.height; y++) {
    for (let x = 0; x < drawing.width; x++) {
      const i = y * drawing.width + x;
      if (drawing.pixels[i] !== decoded.pixels[i]) {
        assert.fail(`pixel mismatch at (${x}, ${y})`);
      }
    }
  }
});

test('compresses the technical drawing below 5% of the 1 bit/px original', () => {
  const drawing = generateTechnicalDrawing();
  const compressed = compress(drawing);
  const raw1bpp = Math.ceil((drawing.width * drawing.height) / 8);
  const ratio = compressed.length / raw1bpp;
  assert.ok(
    ratio < 0.05,
    `ratio ${(ratio * 100).toFixed(2)}% exceeds 5% (${compressed.length} vs ${raw1bpp} bytes)`,
  );
});

test('compress + decode of a full drawing stays within a few milliseconds', () => {
  const drawing = generateTechnicalDrawing();
  const t0 = performance.now();
  const compressed = compress(drawing);
  const t1 = performance.now();
  decompress(compressed);
  const t2 = performance.now();
  const encodeMs = t1 - t0;
  const decodeMs = t2 - t1;
  // generous upper bound so the test is stable on heavily loaded machines;
  // on an idle machine both stages complete in a few milliseconds
  assert.ok(encodeMs < 2000, `encode took ${encodeMs.toFixed(1)} ms`);
  assert.ok(decodeMs < 2000, `decode took ${decodeMs.toFixed(1)} ms`);
});

test('rejects corrupt streams', () => {
  const drawing = generateTechnicalDrawing(200, 120);
  const compressed = compress(drawing);
  assert.throws(() => decompress(compressed.subarray(0, 8)), /too short/);
  const badMagic = compressed.slice();
  badMagic[0] = 0x00;
  assert.throws(() => decompress(badMagic), /bad magic/);
  assert.throws(() => decompress(compressed.subarray(0, compressed.length - 4)));
});

test('rejects a stream whose runs overflow the row width', () => {
  // compress a 5x1 white image, then lie about the width in the header:
  // the stored white run of 5 now overflows the declared row width of 2
  const bmp = createBitmap(5, 1);
  const wide = compress(bmp);
  const view = new DataView(wide.buffer);
  view.setUint32(4, 2, true); // lie about the width: runs now overflow
  assert.throws(() => decompress(wide), /overflows row/);
});
