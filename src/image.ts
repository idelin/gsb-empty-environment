export type Rgba = [number, number, number, number];

export interface RgbaImage {
  width: number;
  height: number;
  pixels: Uint8Array;
}

export interface Tile {
  width: number;
  height: number;
  pixels: Uint8Array;
}

export function createImage(width: number, height: number, fill: Rgba = [0, 0, 0, 0]): RgbaImage {
  const pixels = new Uint8Array(width * height * 4);
  for (let i = 0; i < width * height; i++) {
    pixels[i * 4] = fill[0];
    pixels[i * 4 + 1] = fill[1];
    pixels[i * 4 + 2] = fill[2];
    pixels[i * 4 + 3] = fill[3];
  }
  return { width, height, pixels };
}

export function setPixel(img: RgbaImage, x: number, y: number, c: Rgba): void {
  const o = (y * img.width + x) * 4;
  img.pixels[o] = c[0];
  img.pixels[o + 1] = c[1];
  img.pixels[o + 2] = c[2];
  img.pixels[o + 3] = c[3];
}

export function getPixel(img: RgbaImage, x: number, y: number): Rgba {
  const o = (y * img.width + x) * 4;
  return [img.pixels[o], img.pixels[o + 1], img.pixels[o + 2], img.pixels[o + 3]];
}

export function extractTile(img: RgbaImage, tileX: number, tileY: number, tileSize: number): Tile {
  const x0 = tileX * tileSize;
  const y0 = tileY * tileSize;
  const w = Math.min(tileSize, img.width - x0);
  const h = Math.min(tileSize, img.height - y0);
  const pixels = new Uint8Array(w * h * 4);
  for (let y = 0; y < h; y++) {
    const src = ((y0 + y) * img.width + x0) * 4;
    pixels.set(img.pixels.subarray(src, src + w * 4), y * w * 4);
  }
  return { width: w, height: h, pixels };
}

export function blitTile(img: RgbaImage, tile: Tile, tileX: number, tileY: number, tileSize: number): void {
  const x0 = tileX * tileSize;
  const y0 = tileY * tileSize;
  for (let y = 0; y < tile.height; y++) {
    const dst = ((y0 + y) * img.width + x0) * 4;
    img.pixels.set(tile.pixels.subarray(y * tile.width * 4, (y + 1) * tile.width * 4), dst);
  }
}

export function imagesEqual(a: RgbaImage, b: RgbaImage): boolean {
  if (a.width !== b.width || a.height !== b.height) return false;
  if (a.pixels.length !== b.pixels.length) return false;
  for (let i = 0; i < a.pixels.length; i++) {
    if (a.pixels[i] !== b.pixels[i]) return false;
  }
  return true;
}

export function bytesEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}
