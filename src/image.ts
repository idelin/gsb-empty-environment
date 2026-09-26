import type { RGBA } from "./color.ts";

/**
 * Minimal self-built RGBA image backed by a Uint8ClampedArray.
 * Deliberately does NOT use Canvas, CanvasRenderingContext2D filters,
 * or any image-processing library.
 */
export class RgbaImage {
  readonly width: number;
  readonly height: number;
  readonly data: Uint8ClampedArray;

  constructor(width: number, height: number, data?: ArrayLike<number>) {
    if (!Number.isInteger(width) || !Number.isInteger(height) || width <= 0 || height <= 0) {
      throw new Error(`invalid image size ${width}x${height}`);
    }
    this.width = width;
    this.height = height;
    if (data === undefined) {
      this.data = new Uint8ClampedArray(width * height * 4);
      for (let i = 3; i < this.data.length; i += 4) this.data[i] = 255; // opaque
    } else {
      if (data.length !== width * height * 4) {
        throw new Error(`pixel data length ${data.length} != ${width * height * 4}`);
      }
      this.data = Uint8ClampedArray.from(data as ArrayLike<number>);
    }
  }

  index(x: number, y: number): number {
    if (x < 0 || y < 0 || x >= this.width || y >= this.height) {
      throw new Error(`pixel (${x}, ${y}) out of bounds ${this.width}x${this.height}`);
    }
    return y * this.width + x;
  }

  inBounds(x: number, y: number): boolean {
    return x >= 0 && y >= 0 && x < this.width && y < this.height;
  }

  get(x: number, y: number): RGBA {
    const i = this.index(x, y) * 4;
    return [this.data[i], this.data[i + 1], this.data[i + 2], this.data[i + 3]];
  }

  set(x: number, y: number, rgba: RGBA): void {
    const i = this.index(x, y) * 4;
    this.data[i] = rgba[0];
    this.data[i + 1] = rgba[1];
    this.data[i + 2] = rgba[2];
    this.data[i + 3] = rgba[3];
  }

  fill(rgba: RGBA): void {
    for (let p = 0; p < this.width * this.height; p++) {
      const i = p * 4;
      this.data[i] = rgba[0];
      this.data[i + 1] = rgba[1];
      this.data[i + 2] = rgba[2];
      this.data[i + 3] = rgba[3];
    }
  }
}
