/** Blend modes supported by the compositor. */
export const BlendMode = {
  SourceOver: 'source-over',
  Multiply: 'multiply',
  Additive: 'additive',
} as const;
export type BlendMode = (typeof BlendMode)[keyof typeof BlendMode];

/** Non-premultiplied RGBA8 bitmap, hand-rolled (no Canvas / image libs). */
export class Bitmap {
  readonly width: number;
  readonly height: number;
  readonly data: Uint8ClampedArray;

  constructor(width: number, height: number, data?: Uint8ClampedArray) {
    this.width = width;
    this.height = height;
    this.data = data ?? new Uint8ClampedArray(width * height * 4);
  }

  static from(width: number, height: number, pixels: number[]): Bitmap {
    if (pixels.length !== width * height * 4) {
      throw new Error(`expected ${width * height * 4} components, got ${pixels.length}`);
    }
    return new Bitmap(width, height, new Uint8ClampedArray(pixels));
  }

  clone(): Bitmap {
    return new Bitmap(this.width, this.height, new Uint8ClampedArray(this.data));
  }

  private idx(x: number, y: number): number {
    return (y * this.width + x) * 4;
  }

  getPixel(x: number, y: number): [number, number, number, number] {
    const i = this.idx(x, y);
    return [this.data[i], this.data[i + 1], this.data[i + 2], this.data[i + 3]];
  }

  setPixel(x: number, y: number, r: number, g: number, b: number, a: number): void {
    const i = this.idx(x, y);
    this.data[i] = r;
    this.data[i + 1] = g;
    this.data[i + 2] = b;
    this.data[i + 3] = a;
  }

  fill(r: number, g: number, b: number, a: number): void {
    for (let y = 0; y < this.height; y++) {
      for (let x = 0; x < this.width; x++) this.setPixel(x, y, r, g, b, a);
    }
  }

  equals(other: Bitmap): boolean {
    if (this.width !== other.width || this.height !== other.height) return false;
    for (let i = 0; i < this.data.length; i++) {
      if (this.data[i] !== other.data[i]) return false;
    }
    return true;
  }

  /** Blend src over this pixel in place. All channels 0..255, opacity 0..1. */
  blendPixel(
    x: number,
    y: number,
    sr: number, sg: number, sb: number, sa: number,
    mode: BlendMode,
    opacity: number,
  ): void {
    const i = this.idx(x, y);
    const srcA = (sa / 255) * opacity;
    if (srcA <= 0) return;
    const dstA = this.data[i + 3] / 255;

    let br: number, bg: number, bb: number;
    if (mode === BlendMode.Multiply) {
      br = (sr * this.data[i]) / 255;
      bg = (sg * this.data[i + 1]) / 255;
      bb = (sb * this.data[i + 2]) / 255;
    } else if (mode === BlendMode.Additive) {
      br = Math.min(255, sr + this.data[i]);
      bg = Math.min(255, sg + this.data[i + 1]);
      bb = Math.min(255, sb + this.data[i + 2]);
    } else {
      br = sr; bg = sg; bb = sb;
    }

    // Composite blended color with source-over using srcA.
    const outA = srcA + dstA * (1 - srcA);
    if (outA <= 0) return;
    this.data[i]     = Math.round((br * srcA + this.data[i]     * dstA * (1 - srcA)) / outA);
    this.data[i + 1] = Math.round((bg * srcA + this.data[i + 1] * dstA * (1 - srcA)) / outA);
    this.data[i + 2] = Math.round((bb * srcA + this.data[i + 2] * dstA * (1 - srcA)) / outA);
    this.data[i + 3] = Math.round(outA * 255);
  }
}
