/** 自建 RGBA 小图结构，不依赖 Canvas 或任何图像处理库。 */

export type Color = [number, number, number, number];

export const TRANSPARENT: Color = [0, 0, 0, 0];

export class RGBAImage {
  width: number;
  height: number;
  data: Uint8ClampedArray;

  constructor(width: number, height: number, data?: Uint8ClampedArray) {
    if (!Number.isInteger(width) || !Number.isInteger(height) || width <= 0 || height <= 0) {
      throw new RangeError(`invalid image size ${width}x${height}`);
    }
    this.width = width;
    this.height = height;
    this.data = data ?? new Uint8ClampedArray(width * height * 4);
    if (this.data.length !== width * height * 4) {
      throw new RangeError('data length does not match dimensions');
    }
  }

  byteOffset(x: number, y: number): number {
    if (x < 0 || y < 0 || x >= this.width || y >= this.height) {
      throw new RangeError(`pixel out of bounds (${x},${y}) for ${this.width}x${this.height}`);
    }
    return (y * this.width + x) * 4;
  }

  getPixel(x: number, y: number): Color {
    const o = this.byteOffset(x, y);
    return [this.data[o], this.data[o + 1], this.data[o + 2], this.data[o + 3]];
  }

  setPixel(x: number, y: number, color: Color): void {
    const o = this.byteOffset(x, y);
    this.data[o] = color[0];
    this.data[o + 1] = color[1];
    this.data[o + 2] = color[2];
    this.data[o + 3] = color[3];
  }

  clone(): RGBAImage {
    return new RGBAImage(this.width, this.height, new Uint8ClampedArray(this.data));
  }

  equals(other: RGBAImage): boolean {
    if (this.width !== other.width || this.height !== other.height) return false;
    if (this.data.length !== other.data.length) return false;
    for (let i = 0; i < this.data.length; i++) {
      if (this.data[i] !== other.data[i]) return false;
    }
    return true;
  }

  /** 裁剪出子区域，返回新图。 */
  crop(x: number, y: number, w: number, h: number): RGBAImage {
    if (x < 0 || y < 0 || w <= 0 || h <= 0 || x + w > this.width || y + h > this.height) {
      throw new RangeError(`crop rect (${x},${y},${w},${h}) outside ${this.width}x${this.height}`);
    }
    const out = new RGBAImage(w, h);
    for (let row = 0; row < h; row++) {
      const srcStart = ((y + row) * this.width + x) * 4;
      out.data.set(this.data.subarray(srcStart, srcStart + w * 4), row * w * 4);
    }
    return out;
  }
}
