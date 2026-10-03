/**
 * Self-contained RGBA handling: we build tiny RGBA images in code and decode
 * them to an integer heightmap by hand. No Canvas, no image libraries.
 */

export type RgbaFn = (x: number, y: number) => [number, number, number, number];

export function buildRgba(width: number, height: number, fn: RgbaFn): Uint8Array {
  const buf = new Uint8Array(width * height * 4);
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const [r, g, b, a] = fn(x, y);
      const o = (y * width + x) * 4;
      buf[o] = r; buf[o + 1] = g; buf[o + 2] = b; buf[o + 3] = a;
    }
  }
  return buf;
}

/** Integer luminance decode; rejects truncated buffers and non-opaque alpha. */
export function decodeRgbaToHeights(buf: Uint8Array, width: number, height: number): Int32Array {
  if (buf.length !== width * height * 4) {
    throw new Error(`RGBA buffer size ${buf.length} does not match ${width}x${height}`);
  }
  const out = new Int32Array(width * height);
  for (let p = 0; p < width * height; p++) {
    const o = p * 4;
    if (buf[o + 3] !== 255) throw new Error(`pixel ${p}: alpha must be 255, got ${buf[o + 3]}`);
    out[p] = (buf[o] * 299 + buf[o + 1] * 587 + buf[o + 2] * 114) / 1000 | 0;
  }
  return out;
}

/**
 * Deterministic handcrafted terrain encoded as a gray RGBA image:
 * two smooth bowls (seeded basins), one crater (a seedless basin that must
 * stay unassigned) and a flat plateau band crossing several tiles.
 */
export function sampleTerrainRgba(): { buf: Uint8Array; width: number; height: number } {
  const width = 32, height = 24;
  const buf = buildRgba(width, height, (x, y) => {
    const bowl1 = 20 + 4 * (Math.abs(x - 8) + Math.abs(y - 8));
    const bowl2 = 30 + 4 * (Math.abs(x - 24) + Math.abs(y - 16));
    let h = Math.min(bowl1, bowl2);
    if (Math.abs(x - 22) + Math.abs(y - 6) <= 3) h = 15; // crater: seedless basin
    if (y >= 11 && y <= 13) h = 70;                      // flat plateau band
    h = Math.max(0, Math.min(255, h));
    return [h, h, h, 255];
  });
  return { buf, width, height };
}
