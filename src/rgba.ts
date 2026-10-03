/**
 * Minimal hand-rolled RGBA image used to validate the distance field
 * without Canvas, filters, or any image-processing library. Pixels are
 * plain bytes in a Uint8ClampedArray; decoding is done manually.
 */

export interface RGBAImage {
  width: number;
  height: number;
  /** 4 bytes per pixel, row-major: R, G, B, A. */
  data: Uint8ClampedArray;
}

export function createImage(width: number, height: number): RGBAImage {
  return { width, height, data: new Uint8ClampedArray(width * height * 4) };
}

export function setPixel(
  img: RGBAImage, x: number, y: number,
  r: number, g: number, b: number, a: number,
): void {
  const i = (y * img.width + x) * 4;
  img.data[i] = r;
  img.data[i + 1] = g;
  img.data[i + 2] = b;
  img.data[i + 3] = a;
}

export function fillRect(
  img: RGBAImage, x0: number, y0: number, w: number, h: number,
  r: number, g: number, b: number, a: number,
): void {
  for (let y = y0; y < y0 + h; y++) {
    for (let x = x0; x < x0 + w; x++) {
      if (x >= 0 && y >= 0 && x < img.width && y < img.height) {
        setPixel(img, x, y, r, g, b, a);
      }
    }
  }
}

export function getPixel(img: RGBAImage, x: number, y: number): [number, number, number, number] {
  const i = (y * img.width + x) * 4;
  return [img.data[i], img.data[i + 1], img.data[i + 2], img.data[i + 3]];
}

export type ObstaclePredicate = (r: number, g: number, b: number, a: number) => boolean;

/** Default: a pixel is an obstacle when it is (mostly) opaque. */
export const opaqueIsObstacle: ObstaclePredicate = (_r, _g, _b, a) => a >= 128;

/**
 * Decode an image into obstacle world coordinates. `originX/originY` map
 * image pixel (0,0) onto the world; image pixels map 1:1 onto grid cells.
 */
export function obstaclesFromImage(
  img: RGBAImage,
  predicate: ObstaclePredicate = opaqueIsObstacle,
  originX = 0,
  originY = 0,
): Array<[number, number]> {
  const out: Array<[number, number]> = [];
  for (let y = 0; y < img.height; y++) {
    for (let x = 0; x < img.width; x++) {
      const [r, g, b, a] = getPixel(img, x, y);
      if (predicate(r, g, b, a)) out.push([originX + x, originY + y]);
    }
  }
  return out;
}
