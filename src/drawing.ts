/**
 * Synthetic technical-drawing generator: a large white canvas with
 * borders, a grid, part outlines, circles, hatching, dimension lines
 * and text-like blocks. Used by the demo and the tests.
 */

import type { RasterImage } from './codec.ts';

export function createCanvas(width: number, height: number, background = 0): RasterImage {
  const pixels = new Uint8Array(width * height);
  if (background !== 0) pixels.fill(background);
  return { width, height, bitDepth: 1, pixels };
}

export function setPixel(image: RasterImage, x: number, y: number, value = 1): void {
  if (x < 0 || y < 0 || x >= image.width || y >= image.height) return;
  image.pixels[y * image.width + x] = value;
}

export function hLine(image: RasterImage, x0: number, x1: number, y: number, value = 1): void {
  for (let x = Math.max(0, x0); x <= Math.min(image.width - 1, x1); x++) setPixel(image, x, y, value);
}

export function vLine(image: RasterImage, x: number, y0: number, y1: number, value = 1): void {
  for (let y = Math.max(0, y0); y <= Math.min(image.height - 1, y1); y++) setPixel(image, x, y, value);
}

export function rect(image: RasterImage, x: number, y: number, w: number, h: number, value = 1): void {
  hLine(image, x, x + w - 1, y, value);
  hLine(image, x, x + w - 1, y + h - 1, value);
  vLine(image, x, y, y + h - 1, value);
  vLine(image, x + w - 1, y, y + h - 1, value);
}

export function circle(image: RasterImage, cx: number, cy: number, r: number, value = 1): void {
  let x = r;
  let y = 0;
  let err = 1 - r;
  while (x >= y) {
    setPixel(image, cx + x, cy + y, value);
    setPixel(image, cx + y, cy + x, value);
    setPixel(image, cx - y, cy + x, value);
    setPixel(image, cx - x, cy + y, value);
    setPixel(image, cx - x, cy - y, value);
    setPixel(image, cx - y, cy - x, value);
    setPixel(image, cx + y, cy - x, value);
    setPixel(image, cx + x, cy - y, value);
    y++;
    if (err < 0) {
      err += 2 * y + 1;
    } else {
      x--;
      err += 2 * (y - x) + 1;
    }
  }
}

/** Deterministic PRNG (mulberry32) so drawings are reproducible. */
export function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * A 1-bit "engineering sheet": ~1-2% black coverage on a large canvas,
 * the classic best case for RLE+Huffman fax-style compression.
 */
export function makeTechnicalDrawing(width: number, height: number, seed = 42): RasterImage {
  const image = createCanvas(width, height);
  const rand = mulberry32(seed);
  const m = 24; // margin

  // Title frame.
  rect(image, m, m, width - 2 * m, height - 2 * m);

  // Faint construction grid every 512 px (2 px dots, 8 px pitch).
  for (let x = m + 512; x < width - m; x += 512) {
    for (let y = m + 8; y < height - m - 8; y += 8) {
      setPixel(image, x, y);
      setPixel(image, x, y + 1);
    }
  }
  for (let y = m + 512; y < height - m; y += 512) {
    for (let x = m + 8; x < width - m - 8; x += 8) {
      setPixel(image, x, y);
      setPixel(image, x + 1, y);
    }
  }

  // Part outlines.
  rect(image, width * 0.08, height * 0.12, width * 0.28, height * 0.2);
  rect(image, width * 0.58, height * 0.16, width * 0.3, height * 0.14);
  circle(image, width * 0.22, height * 0.22, Math.min(width, height) * 0.06);
  circle(image, width * 0.73, height * 0.23, Math.min(width, height) * 0.04);
  circle(image, width * 0.73, height * 0.23, Math.min(width, height) * 0.015);

  // Hatched region (45-degree lines, 28 px pitch).
  const hx = width * 0.1;
  const hy = height * 0.62;
  const hw = width * 0.13;
  const hh = height * 0.13;
  rect(image, hx, hy, hw, hh);
  for (let d = -hh; d < hw; d += 28) {
    for (let t = 0; t < hh; t++) {
      const x = Math.round(hx + d + t);
      const y = Math.round(hy + t);
      if (x >= hx && x < hx + hw) setPixel(image, x, y);
    }
  }

  // Dimension lines with arrowheads.
  const dy = Math.round(height * 0.42);
  hLine(image, width * 0.08, width * 0.36, dy);
  vLine(image, width * 0.08, dy - 8, dy + 8);
  vLine(image, width * 0.36, dy - 8, dy + 8);

  // Text-like blocks (pseudo glyphs: short vertical runs in rows).
  for (let row = 0; row < 2; row++) {
    const ty = Math.round(height * 0.5) + row * 16;
    let tx = Math.round(width * 0.55);
    while (tx < width * 0.8) {
      const glyphWidth = 4 + Math.floor(rand() * 5);
      for (let gx = 0; gx < glyphWidth; gx++) {
        for (let gy = 0; gy < 9; gy++) {
          if (rand() < 0.25) setPixel(image, tx + gx, ty + gy);
        }
      }
      tx += glyphWidth + 3;
    }
  }

  // Scattered annotation marks.
  for (let i = 0; i < 20; i++) {
    const x = m + 8 + Math.floor(rand() * (width - 2 * m - 16));
    const y = m + 8 + Math.floor(rand() * (height - 2 * m - 16));
    rect(image, x, y, 3 + Math.floor(rand() * 8), 3 + Math.floor(rand() * 5));
  }

  return image;
}

/** Black-coverage ratio of a binary image (0..1). */
export function inkCoverage(image: RasterImage): number {
  let black = 0;
  for (let i = 0; i < image.pixels.length; i++) if (image.pixels[i] !== 0) black++;
  return black / image.pixels.length;
}
