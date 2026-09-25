/** Minimal self-contained RGBA image. No canvas, no image libraries. */
export type Color = [number, number, number, number];

export class RgbaImage {
  width: number;
  height: number;
  data: Uint8ClampedArray; // RGBA, row-major, 4 bytes per pixel

  constructor(width: number, height: number, data?: Uint8ClampedArray) {
    if (!Number.isInteger(width) || !Number.isInteger(height) || width <= 0 || height <= 0) {
      throw new Error(`invalid image size ${width}x${height}`);
    }
    this.width = width;
    this.height = height;
    this.data = data ?? new Uint8ClampedArray(width * height * 4);
    if (this.data.length !== width * height * 4) {
      throw new Error('data length does not match dimensions');
    }
  }

  clone(): RgbaImage {
    return new RgbaImage(this.width, this.height, new Uint8ClampedArray(this.data));
  }

  offset(x: number, y: number): number {
    return (y * this.width + x) * 4;
  }

  getPixel(x: number, y: number): Color {
    const o = this.offset(x, y);
    return [this.data[o], this.data[o + 1], this.data[o + 2], this.data[o + 3]];
  }

  setPixel(x: number, y: number, color: Color): void {
    const o = this.offset(x, y);
    this.data[o] = color[0];
    this.data[o + 1] = color[1];
    this.data[o + 2] = color[2];
    this.data[o + 3] = color[3];
  }

  fill(color: Color): void {
    for (let i = 0; i < this.data.length; i += 4) {
      this.data[i] = color[0];
      this.data[i + 1] = color[1];
      this.data[i + 2] = color[2];
      this.data[i + 3] = color[3];
    }
  }

  equals(other: RgbaImage): boolean {
    if (this.width !== other.width || this.height !== other.height) return false;
    if (this.data.length !== other.data.length) return false;
    for (let i = 0; i < this.data.length; i++) {
      if (this.data[i] !== other.data[i]) return false;
    }
    return true;
  }
}
