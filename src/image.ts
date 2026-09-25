import type { Mask } from './raster.ts';

export type Color = [number, number, number, number];

export class RgbaImage {
  readonly width: number;
  readonly height: number;
  readonly data: Uint8ClampedArray;

  constructor(width: number, height: number, background: Color = [0, 0, 0, 0]) {
    this.width = width;
    this.height = height;
    this.data = new Uint8ClampedArray(width * height * 4);
    for (let i = 0; i < width * height; i++) {
      this.data[i * 4] = background[0];
      this.data[i * 4 + 1] = background[1];
      this.data[i * 4 + 2] = background[2];
      this.data[i * 4 + 3] = background[3];
    }
  }

  pixel(x: number, y: number): Color {
    const i = (y * this.width + x) * 4;
    return [this.data[i], this.data[i + 1], this.data[i + 2], this.data[i + 3]];
  }

  composite(mask: Mask, color: Color): void {
    if (mask.width !== this.width || mask.height !== this.height) {
      throw new Error('mask size must match image size');
    }
    for (let i = 0; i < mask.data.length; i++) {
      const coverage = mask.data[i];
      if (coverage <= 0) continue;
      const srcA = (color[3] / 255) * coverage;
      const dstA = this.data[i * 4 + 3] / 255;
      const outA = srcA + dstA * (1 - srcA);
      for (let ch = 0; ch < 3; ch++) {
        const src = color[ch] / 255;
        const dst = this.data[i * 4 + ch] / 255;
        const out = outA === 0 ? 0 : (src * srcA + dst * dstA * (1 - srcA)) / outA;
        this.data[i * 4 + ch] = out * 255;
      }
      this.data[i * 4 + 3] = outA * 255;
    }
  }
}
