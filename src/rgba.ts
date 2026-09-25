/**
 * Minimal dependency-free RGBA image model.
 *
 * Pixels are packed RGBA, row-major, one byte per channel.
 * A pixel with alpha === 0 is "transparent"; its RGB bytes are kept as
 * authored but are ignored by the comparison semantics (see diff.ts).
 */

export type RGBA = readonly [number, number, number, number];

export interface RGBAImage {
  readonly width: number;
  readonly height: number;
  readonly data: Uint8Array;
}

export const TRANSPARENT: RGBA = [0, 0, 0, 0];

export function createImage(width: number, height: number, fill: RGBA = TRANSPARENT): RGBAImage {
  if (!Number.isInteger(width) || !Number.isInteger(height) || width < 1 || height < 1) {
    throw new Error(`invalid image size ${width}x${height}`);
  }
  const data = new Uint8Array(width * height * 4);
  for (let i = 0; i < width * height; i++) {
    data[i * 4] = fill[0];
    data[i * 4 + 1] = fill[1];
    data[i * 4 + 2] = fill[2];
    data[i * 4 + 3] = fill[3];
  }
  return { width, height, data };
}

export function cloneImage(img: RGBAImage): RGBAImage {
  return { width: img.width, height: img.height, data: new Uint8Array(img.data) };
}

export function inBounds(img: RGBAImage, x: number, y: number): boolean {
  return x >= 0 && y >= 0 && x < img.width && y < img.height;
}

export function getPixel(img: RGBAImage, x: number, y: number): RGBA {
  if (!inBounds(img, x, y)) throw new Error(`pixel out of bounds: (${x}, ${y})`);
  const i = (y * img.width + x) * 4;
  return [img.data[i], img.data[i + 1], img.data[i + 2], img.data[i + 3]];
}

export function setPixel(img: RGBAImage, x: number, y: number, px: RGBA): void {
  if (!inBounds(img, x, y)) throw new Error(`pixel out of bounds: (${x}, ${y})`);
  const i = (y * img.width + x) * 4;
  img.data[i] = px[0];
  img.data[i + 1] = px[1];
  img.data[i + 2] = px[2];
  img.data[i + 3] = px[3];
}

/** Build an image from string rows and a char -> RGBA palette (handy for tests). */
export function fromStrings(rows: readonly string[], palette: Record<string, RGBA>): RGBAImage {
  if (rows.length === 0) throw new Error('fromStrings: no rows');
  const width = rows[0].length;
  if (width === 0) throw new Error('fromStrings: empty row');
  const img = createImage(width, rows.length);
  rows.forEach((row, y) => {
    if (row.length !== width) throw new Error(`fromStrings: ragged row ${y}`);
    for (let x = 0; x < width; x++) {
      const px = palette[row[x]];
      if (!px) throw new Error(`fromStrings: no palette entry for '${row[x]}'`);
      setPixel(img, x, y, px);
    }
  });
  return img;
}

export interface TilePlacement {
  readonly x: number;
  readonly y: number;
  readonly image: RGBAImage;
}

/**
 * Assemble an image from tiles. Later tiles overwrite earlier ones where they
 * overlap; for non-overlapping tiles the result is independent of tile order.
 */
export function composeTiles(
  width: number,
  height: number,
  tiles: readonly TilePlacement[],
  fill: RGBA = TRANSPARENT,
): RGBAImage {
  const out = createImage(width, height, fill);
  for (const tile of tiles) {
    for (let y = 0; y < tile.image.height; y++) {
      for (let x = 0; x < tile.image.width; x++) {
        const tx = tile.x + x;
        const ty = tile.y + y;
        if (!inBounds(out, tx, ty)) {
          throw new Error(`tile at (${tile.x}, ${tile.y}) exceeds canvas ${width}x${height}`);
        }
        setPixel(out, tx, ty, getPixel(tile.image, x, y));
      }
    }
  }
  return out;
}

/** Deterministic PRNG (mulberry32) for reproducible test fixtures. */
export function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * Random image for property tests. `transparentRate` (0..1) gives a fraction
 * of pixels alpha 0 with *random* RGB left behind, to exercise the rule that
 * transparent RGB is ignored.
 */
export function randomImage(
  width: number,
  height: number,
  rand: () => number,
  transparentRate = 0,
): RGBAImage {
  const img = createImage(width, height);
  for (let i = 0; i < width * height; i++) {
    img.data[i * 4] = Math.floor(rand() * 256);
    img.data[i * 4 + 1] = Math.floor(rand() * 256);
    img.data[i * 4 + 2] = Math.floor(rand() * 256);
    img.data[i * 4 + 3] = rand() < transparentRate ? 0 : 255;
  }
  return img;
}
