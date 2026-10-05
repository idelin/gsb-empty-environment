/**
 Minimal self-contained RGBA image helpers.
 * No canvas, no image processing libraries: images are plain typed arrays
 * built pixel by pixel, and heights are derived with integer arithmetic only.
 */
export interface RGBAImage {
  width: number;
  height: number;
  data: Uint8ClampedArray;
}

export type RGBA = [number, number, number, number];

export function createImage(width: number, height: number, fill: RGBA = [0, 0, 0, 255]): RGBAImage {
  const data = new Uint8ClampedArray(width * height * 4);
  for (let i = 0; i < width * height; i++) {
    data[i * 4] = fill[0];
    data[i * 4 + 1] = fill[1];
    data[i * 4 + 2] = fill[2];
    data[i * 4 + 3] = fill[3];
  }
  return { width, height, data };
}

export function setPixel(img: RGBAImage, x: number, y: number, rgba: RGBA): void {
  const o = (y * img.width + x) * 4;
  img.data[o] = rgba[0];
  img.data[o + 1] = rgba[1];
  img.data[o + 2] = rgba[2];
  img.data[o + 3] = rgba[3];
}

export function fillRect(img: RGBAImage, x: number, y: number, w: number, h: number, rgba: RGBA): void {
  for (let yy = y; yy < y + h; yy++) {
    for (let xx = x; xx < x + w; xx++) {
      setPixel(img, xx, yy, rgba);
    }
  }
}

/** Builds an RGBA image whose R=G=B channels come from `shade(x, y)` (0..255). */
export function grayImage(width: number, height: number, shade: (x: number, y: number) => number): RGBAImage {
  const img = createImage(width, height);
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const v = Math.max(0, Math.min(255, Math.round(shade(x, y))));
      setPixel(img, x, y, [v, v, v, 255]);
    }
  }
  return img;
}

export type Channel = 'luma' | 'r' | 'g' | 'b' | 'a';

/**
 * Converts an RGBA image to an integer heightmap.
 * Luma uses the integer approximation (77R + 150G + 29B) >> 8.
 */
export function heightsFromRGBA(img: RGBAImage, channel: Channel = 'luma'): Int32Array {
  const { width, height, data } = img;
  const out = new Int32Array(width * height);
  for (let i = 0; i < width * height; i++) {
    const r = data[i * 4];
    const g = data[i * 4 + 1];
    const b = data[i * 4 + 2];
    const a = data[i * 4 + 3];
    switch (channel) {
      case 'luma':
        out[i] = (77 * r + 150 * g + 29 * b) >> 8;
        break;
      case 'r':
        out[i] = r;
        break;
      case 'g':
        out[i] = g;
        break;
      case 'b':
        out[i] = b;
        break;
      case 'a':
        out[i] = a;
        break;
    }
  }
  return out;
}
