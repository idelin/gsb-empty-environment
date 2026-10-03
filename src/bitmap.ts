/**
 * Binary raster image: row-major 1-D pixel array, 0 = white, 1 = black.
 * The 1-D layout keeps the raster matrix strictly aligned: pixel (x, y)
 * lives at pixels[y * width + x].
 */
export interface BinaryBitmap {
  readonly width: number;
  readonly height: number;
  readonly pixels: Uint8Array;
}

export function createBitmap(width: number, height: number, fill: 0 | 1 = 0): BinaryBitmap {
  if (!Number.isInteger(width) || !Number.isInteger(height) || width < 0 || height < 0) {
    throw new Error(`Invalid bitmap dimensions ${width}x${height}`);
  }
  const pixels = new Uint8Array(width * height);
  if (fill === 1) pixels.fill(1);
  return { width, height, pixels };
}

export function bitmapsEqual(a: BinaryBitmap, b: BinaryBitmap): boolean {
  if (a.width !== b.width || a.height !== b.height) return false;
  if (a.pixels.length !== b.pixels.length) return false;
  for (let i = 0; i < a.pixels.length; i++) {
    if (a.pixels[i] !== b.pixels[i]) return false;
  }
  return true;
}
