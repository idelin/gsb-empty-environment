import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  compress,
  decompress,
  inspect,
  packBits,
  rawSizeBytes,
  type RasterImage,
} from '../src/index.ts';
import {
  buildHuffmanTable,
  presetTables,
  whiteRunFrequencies,
  MAX_MAKEUP_LENGTH,
} from '../src/huffman.ts';
import { createCanvas, makeTechnicalDrawing, mulberry32, setPixel } from '../src/drawing.ts';

function assertImagesEqual(actual: RasterImage, expected: RasterImage): void {
  assert.equal(actual.width, expected.width);
  assert.equal(actual.height, expected.height);
  assert.equal(actual.bitDepth, expected.bitDepth);
  assert.equal(actual.pixels.length, expected.pixels.length);
  for (let i = 0; i < actual.pixels.length; i++) {
    if (actual.pixels[i] !== expected.pixels[i]) {
      const x = i % actual.width;
      const y = Math.floor(i / actual.width);
      assert.fail(`pixel mismatch at (${x}, ${y}): got ${actual.pixels[i]}, want ${expected.pixels[i]}`);
    }
  }
}

function roundTrip(image: RasterImage): RasterImage {
  return decompress(compress(image));
}

test('round-trip: all-white image (best case)', () => {
  const image = createCanvas(641, 13);
  const compressed = compress(image);
  assertImagesEqual(decompress(compressed), image);
  // 641x13 white pixels collapse into a handful of bytes.
  assert.ok(compressed.length < 64, `expected tiny output, got ${compressed.length} bytes`);
});

test('round-trip: all-black image', () => {
  const image = createCanvas(320, 7);
  image.pixels.fill(1);
  assertImagesEqual(roundTrip(image), image);
});

test('round-trip: checkerboard (worst case, still lossless)', () => {
  const image = createCanvas(127, 64);
  for (let y = 0; y < image.height; y++) {
    for (let x = 0; x < image.width; x++) {
      image.pixels[y * image.width + x] = (x + y) & 1;
    }
  }
  assertImagesEqual(roundTrip(image), image);
});

test('round-trip: deterministic sparse noise', () => {
  const rand = mulberry32(1234);
  const image = createCanvas(513, 129);
  for (let i = 0; i < image.pixels.length; i++) {
    if (rand() < 0.02) image.pixels[i] = 1;
  }
  assertImagesEqual(roundTrip(image), image);
});

test('round-trip: rows starting with black pixels (zero-length white run)', () => {
  const image = createCanvas(50, 3);
  for (let y = 0; y < 3; y++) {
    setPixel(image, 0, y);
    setPixel(image, 49, y);
  }
  assertImagesEqual(roundTrip(image), image);
});

test('round-trip: runs longer than the largest make-up code', () => {
  const image = createCanvas(20000, 2); // 20000 > 4032 = MAX_MAKEUP_LENGTH
  setPixel(image, 19999, 1);
  assert.ok(MAX_MAKEUP_LENGTH === 4032);
  assertImagesEqual(roundTrip(image), image);
});

test('round-trip: low colour depth (2-bit and 4-bit)', () => {
  const rand = mulberry32(99);
  for (const bitDepth of [2, 4] as const) {
    const width = 200;
    const height = 30;
    const max = (1 << bitDepth) - 1;
    const pixels = new Uint8Array(width * height);
    let i = 0;
    while (i < pixels.length) {
      const value = Math.floor(rand() * (max + 1));
      const run = 1 + Math.floor(rand() * 40);
      pixels.fill(value, i, Math.min(i + run, pixels.length));
      i += run;
    }
    const image: RasterImage = { width, height, bitDepth, pixels };
    assertImagesEqual(roundTrip(image), image);
  }
});

test('acceptance: technical drawing compresses below 5% and restores exactly', () => {
  const drawing = makeTechnicalDrawing(2048, 2048);
  const compressed = compress(drawing);
  const restored = decompress(compressed);

  assertImagesEqual(restored, drawing); // strictly aligned pixel matrix
  const ratio = compressed.length / rawSizeBytes(drawing);
  assert.ok(
    ratio < 0.05,
    `expected ratio < 5%, got ${(ratio * 100).toFixed(2)}% (${compressed.length} bytes)`,
  );
});

test('acceptance: encode + decode of a 2048x2048 drawing takes milliseconds', () => {
  const drawing = makeTechnicalDrawing(2048, 2048);
  compress(drawing); // warm up JIT + preset tables

  // Regression guard against pathological slowness. On a quiet machine
  // encode/decode each take a few milliseconds; the generous ceiling
  // keeps the test stable on heavily loaded CI hosts.
  let encodeMs = Infinity;
  let decodeMs = Infinity;
  let compressed: Uint8Array = new Uint8Array(0);
  for (let attempt = 0; attempt < 3; attempt++) {
    const t0 = performance.now();
    compressed = compress(drawing);
    const t1 = performance.now();
    const restored = decompress(compressed);
    const t2 = performance.now();
    encodeMs = Math.min(encodeMs, t1 - t0);
    decodeMs = Math.min(decodeMs, t2 - t1);
    assertImagesEqual(restored, drawing);
  }
  console.log(`    encode ${encodeMs.toFixed(2)} ms, decode ${decodeMs.toFixed(2)} ms (best of 3)`);
  assert.ok(encodeMs < 500, `encode too slow: ${encodeMs.toFixed(1)} ms`);
  assert.ok(decodeMs < 500, `decode too slow: ${decodeMs.toFixed(1)} ms`);
});

test('header: inspect reports dimensions without decoding', () => {
  const drawing = makeTechnicalDrawing(300, 120);
  const header = inspect(compress(drawing));
  assert.deepEqual(header, { width: 300, height: 120, bitDepth: 1 });
});

test('packBits produces a packed 1-bit bitmap', () => {
  const image = createCanvas(8, 1);
  setPixel(image, 0, 0);
  setPixel(image, 3, 0);
  setPixel(image, 7, 0);
  assert.deepEqual([...packBits(image)], [0b10010001]);
});

test('corrupt streams are rejected', () => {
  const drawing = makeTechnicalDrawing(128, 64);
  const compressed = compress(drawing);

  assert.throws(() => decompress(compressed.subarray(0, compressed.length - 2)), /end of stream|Corrupt/);
  const badMagic = compressed.slice();
  badMagic[0] = 0x00;
  assert.throws(() => decompress(badMagic), /bad magic/);
  assert.throws(() => decompress(new Uint8Array(4)), /too short/);
});

test('invalid images are rejected by the encoder', () => {
  assert.throws(
    () => compress({ width: 0, height: 10, bitDepth: 1, pixels: new Uint8Array(0) }),
    /Invalid width/,
  );
  assert.throws(
    () => compress({ width: 4, height: 4, bitDepth: 1, pixels: new Uint8Array(3) }),
    /does not match/,
  );
  assert.throws(
    () => compress({ width: 2, height: 2, bitDepth: 1, pixels: Uint8Array.of(0, 1, 0, 2) }),
    /exceeds bitDepth/,
  );
});

test('huffman: preset tables are deterministic and prefix-free', () => {
  const a = buildHuffmanTable(whiteRunFrequencies());
  const b = buildHuffmanTable(whiteRunFrequencies());
  assert.deepEqual([...a.codes], [...b.codes]);
  assert.deepEqual([...a.lengths], [...b.lengths]);

  // No code may be a prefix of another code.
  for (const table of [presetTables().white, presetTables().black, presetTables().neutral]) {
    const seen = new Map<string, number>();
    for (let s = 0; s < table.codes.length; s++) {
      seen.set(table.codes[s].toString(2).padStart(table.lengths[s], '0'), s);
    }
    const codes = [...seen.keys()];
    for (let i = 0; i < codes.length; i++) {
      for (let j = 0; j < codes.length; j++) {
        if (i !== j) {
          assert.ok(!codes[j].startsWith(codes[i]), `${codes[i]} is a prefix of ${codes[j]}`);
        }
      }
    }
  }
});
