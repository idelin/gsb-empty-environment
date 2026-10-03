import { createBitmap, type BinaryBitmap } from '../src/bitmap.ts';

/** Deterministic PRNG (mulberry32) so the sample drawing is reproducible. */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function setPixel(bmp: BinaryBitmap, x: number, y: number): void {
  if (x >= 0 && x < bmp.width && y >= 0 && y < bmp.height) {
    bmp.pixels[y * bmp.width + x] = 1;
  }
}

function hline(bmp: BinaryBitmap, x0: number, x1: number, y: number, thickness = 1): void {
  for (let t = 0; t < thickness; t++) {
    for (let x = x0; x <= x1; x++) setPixel(bmp, x, y + t);
  }
}

function vline(bmp: BinaryBitmap, x: number, y0: number, y1: number, thickness = 1): void {
  for (let t = 0; t < thickness; t++) {
    for (let y = y0; y <= y1; y++) setPixel(bmp, x + t, y);
  }
}

function rect(bmp: BinaryBitmap, x: number, y: number, w: number, h: number, thickness = 1): void {
  hline(bmp, x, x + w - 1, y, thickness);
  hline(bmp, x, x + w - 1, y + h - thickness, thickness);
  vline(bmp, x, y, y + h - 1, thickness);
  vline(bmp, x + w - thickness, y, y + h - 1, thickness);
}

function circle(bmp: BinaryBitmap, cx: number, cy: number, r: number, thickness = 1): void {
  const r2 = r * r;
  const inner = (r - thickness) * (r - thickness);
  for (let y = cy - r; y <= cy + r; y++) {
    for (let x = cx - r; x <= cx + r; x++) {
      const d2 = (x - cx) * (x - cx) + (y - cy) * (y - cy);
      if (d2 <= r2 && d2 > inner) setPixel(bmp, x, y);
    }
  }
}

function line(
  bmp: BinaryBitmap,
  x0: number,
  y0: number,
  x1: number,
  y1: number,
  thickness = 1,
): void {
  const dx = Math.abs(x1 - x0);
  const dy = Math.abs(y1 - y0);
  const sx = x0 < x1 ? 1 : -1;
  const sy = y0 < y1 ? 1 : -1;
  let err = dx - dy;
  let x = x0;
  let y = y0;
  for (;;) {
    for (let t = 0; t < thickness; t++) setPixel(bmp, x + t, y);
    if (x === x1 && y === y1) break;
    const e2 = 2 * err;
    if (e2 > -dy) {
      err -= dy;
      x += sx;
    }
    if (e2 < dx) {
      err += dx;
      y += sy;
    }
  }
}

function hatch(bmp: BinaryBitmap, x: number, y: number, w: number, h: number, spacing: number): void {
  for (let d = -h; d < w; d += spacing) {
    line(bmp, x + d, y, x + d + h, y + h);
  }
}

/** Simulates paragraphs of text as rows of short black bars. */
function textBlock(
  bmp: BinaryBitmap,
  x: number,
  y: number,
  maxWidth: number,
  lineCount: number,
  rng: () => number,
): void {
  for (let i = 0; i < lineCount; i++) {
    const lineY = y + i * 26;
    let cursor = x;
    const end = x + maxWidth - Math.floor(rng() * maxWidth * 0.3);
    while (cursor < end) {
      const word = 12 + Math.floor(rng() * 48);
      hline(bmp, cursor, Math.min(cursor + word, x + maxWidth), lineY, 5);
      cursor += word + 8 + Math.floor(rng() * 8);
    }
  }
}

function arrow(bmp: BinaryBitmap, x0: number, y0: number, x1: number, y1: number): void {
  line(bmp, x0, y0, x1, y1);
  line(bmp, x1, y1, x1 - 12, y1 - 6);
  line(bmp, x1, y1, x1 - 12, y1 + 6);
  line(bmp, x0, y0, x0 + 12, y0 - 6);
  line(bmp, x0, y0, x0 + 12, y0 + 6);
}

/**
 * Generates a synthetic A4-like technical drawing (300 dpi) with a large
 * amount of whitespace: frame, title block, part outlines, a hatched
 * section view, dimension arrows and pseudo text.
 */
export function generateTechnicalDrawing(width = 3508, height = 2480): BinaryBitmap {
  const bmp = createBitmap(width, height);
  const rng = mulberry32(20261003);

  // Drawing frame
  rect(bmp, 40, 40, width - 80, height - 80, 4);
  rect(bmp, 64, 64, width - 128, height - 128, 1);

  // Title block (bottom-right) with a table grid
  const tbX = width - 900;
  const tbY = height - 320;
  rect(bmp, tbX, tbY, 836, 256, 2);
  for (let i = 1; i < 4; i++) hline(bmp, tbX, tbX + 835, tbY + i * 64, 1);
  for (let i = 1; i < 5; i++) vline(bmp, tbX + i * 167, tbY, tbY + 255, 1);
  textBlock(bmp, tbX + 16, tbY + 20, 130, 8, rng);

  // Part outlines
  rect(bmp, 300, 300, 900, 620, 3);
  rect(bmp, 420, 420, 660, 380, 1);
  circle(bmp, 750, 610, 150, 3);
  circle(bmp, 750, 610, 60, 2);

  rect(bmp, 1600, 260, 1100, 700, 3);
  circle(bmp, 2150, 610, 260, 3);
  circle(bmp, 2150, 610, 110, 2);
  vline(bmp, 2150, 280, 940, 1);
  hline(bmp, 1620, 2680, 610, 1);

  // Hatched section view
  rect(bmp, 300, 1200, 700, 420, 2);
  hatch(bmp, 302, 1202, 696, 416, 64);

  // Dimension arrows
  arrow(bmp, 300, 1020, 1200, 1020);
  arrow(bmp, 1600, 1020, 2700, 1020);
  arrow(bmp, 1300, 300, 1300, 920);

  // Notes / pseudo text
  textBlock(bmp, 1600, 1200, 900, 7, rng);
  textBlock(bmp, 300, 1900, 1200, 5, rng);

  // Center marks
  for (const [cx, cy] of [
    [750, 610],
    [2150, 610],
  ]) {
    hline(bmp, cx - 40, cx + 40, cy, 1);
    vline(bmp, cx, cy - 40, cy + 40, 1);
  }

  return bmp;
}
