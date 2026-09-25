/** Shared value types. All modules are pure and deterministic. */

export interface RGBAImage {
  width: number;
  height: number;
  /** Packed RGBA bytes, length === width * height * 4. */
  data: Uint8ClampedArray;
}

/** Index used in indexed output for fully transparent pixels (alpha === 0). */
export const TRANSPARENT_INDEX = -1;

export type RGB = [number, number, number];

export interface Palette {
  /** Opaque colors only. Transparent never consumes an entry. */
  colors: RGB[];
}

export interface IndexedImage {
  width: number;
  height: number;
  /** One palette index per pixel; TRANSPARENT_INDEX for transparent pixels. */
  indices: Int32Array;
}

export function makeImage(width: number, height: number): RGBAImage {
  return { width, height, data: new Uint8ClampedArray(width * height * 4) };
}

export function setPixel(img: RGBAImage, x: number, y: number, r: number, g: number, b: number, a = 255): void {
  const i = (y * img.width + x) * 4;
  img.data[i] = r;
  img.data[i + 1] = g;
  img.data[i + 2] = b;
  img.data[i + 3] = a;
}

export function isTransparent(img: RGBAImage, x: number, y: number): boolean {
  return img.data[(y * img.width + x) * 4 + 3] === 0;
}
