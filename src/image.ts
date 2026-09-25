export interface RgbaImage {
  width: number;
  height: number;
  data: Uint8Array;
}

export type RgbaPixel = readonly [number, number, number, number];

export const DEFAULT_ALPHA_THRESHOLD = 128;

export function createRgbaImage(
  width: number,
  height: number,
  background: RgbaPixel = [0, 0, 0, 0],
): RgbaImage {
  if (!Number.isInteger(width) || width <= 0) {
    throw new Error("width must be a positive integer");
  }
  if (!Number.isInteger(height) || height <= 0) {
    throw new Error("height must be a positive integer");
  }

  const data = new Uint8Array(width * height * 4);
  for (let i = 0; i < width * height; i += 1) {
    data.set(background, i * 4);
  }
  return { width, height, data };
}

export function setPixel(image: RgbaImage, x: number, y: number, pixel: RgbaPixel): void {
  if (!Number.isInteger(x) || x < 0 || x >= image.width) {
    throw new RangeError(`x is outside the image: ${x}`);
  }
  if (!Number.isInteger(y) || y < 0 || y >= image.height) {
    throw new RangeError(`y is outside the image: ${y}`);
  }
  image.data.set(pixel, (y * image.width + x) * 4);
}

export function rasterizeRgba(
  lines: readonly string[],
  palette: Readonly<Record<string, RgbaPixel>> = {
    ".": [0, 0, 0, 0],
    "#": [0, 0, 0, 255],
  },
): RgbaImage {
  const height = lines.length;
  const width = lines[0]?.length ?? 0;
  if (height === 0 || width === 0) {
    throw new Error("raster input must not be empty");
  }
  if (lines.some((line) => line.length !== width)) {
    throw new Error("all raster rows must have the same width");
  }

  const image = createRgbaImage(width, height);
  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      const symbol = lines[y][x];
      const color = palette[symbol];
      if (!color) {
        throw new Error(`unknown raster symbol: ${symbol}`);
      }
      setPixel(image, x, y, color);
    }
  }
  return image;
}

export interface ForegroundOptions {
  alphaThreshold?: number;
  isForeground?: (pixel: { r: number; g: number; b: number; a: number }, x: number, y: number) => boolean;
}

export function rgbaToMask(image: RgbaImage, options: ForegroundOptions = {}): Uint8Array {
  const threshold = options.alphaThreshold ?? DEFAULT_ALPHA_THRESHOLD;
  if (threshold < 0 || threshold > 255) {
    throw new RangeError("alphaThreshold must be between 0 and 255");
  }

  const mask = new Uint8Array(image.width * image.height);
  for (let y = 0; y < image.height; y += 1) {
    for (let x = 0; x < image.width; x += 1) {
      const offset = (y * image.width + x) * 4;
      const pixel = {
        r: image.data[offset],
        g: image.data[offset + 1],
        b: image.data[offset + 2],
        a: image.data[offset + 3],
      };
      const foreground = options.isForeground
        ? options.isForeground(pixel, x, y)
        : pixel.a >= threshold;
      mask[y * image.width + x] = foreground ? 1 : 0;
    }
  }
  return mask;
}

export function maskToRgba(
  mask: Uint8Array,
  width: number,
  height: number,
  foreground: RgbaPixel = [0, 0, 0, 255],
  background: RgbaPixel = [0, 0, 0, 0],
): RgbaImage {
  if (mask.length !== width * height) {
    throw new Error("mask dimensions do not match");
  }
  const image = createRgbaImage(width, height, background);
  for (let i = 0; i < mask.length; i += 1) {
    if (mask[i]) {
      image.data.set(foreground, i * 4);
    }
  }
  return image;
}
