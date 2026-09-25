import { maskFromRgba, type Mask } from "./mask.ts";

/**
 * Minimal RGBA image for building test fixtures by hand: a width*height*4
 * byte buffer plus a couple of paint helpers. No canvas, no image libraries.
 */
export class RgbaImage {
  readonly width: number;
  readonly height: number;
  readonly data: Uint8ClampedArray;

  constructor(width: number, height: number) {
    this.width = width;
    this.height = height;
    this.data = new Uint8ClampedArray(width * height * 4); // transparent black
  }

  set(x: number, y: number, r: number, g: number, b: number, a: number): this {
    const o = (y * this.width + x) * 4;
    this.data[o] = r;
    this.data[o + 1] = g;
    this.data[o + 2] = b;
    this.data[o + 3] = a;
    return this;
  }

  /** Paint an opaque white pixel (foreground). */
  fg(x: number, y: number): this {
    return this.set(x, y, 255, 255, 255, 255);
  }

  fillRect(x: number, y: number, w: number, h: number): this {
    for (let dy = 0; dy < h; dy++) {
      for (let dx = 0; dx < w; dx++) {
        this.fg(x + dx, y + dy);
      }
    }
    return this;
  }

  /** Erase a pixel back to transparent (background). */
  clear(x: number, y: number): this {
    return this.set(x, y, 0, 0, 0, 0);
  }

  toMask(): Mask {
    return maskFromRgba(this.data, this.width, this.height);
  }
}
