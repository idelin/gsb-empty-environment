import { srgbByteToLinear, linearToSrgbByte } from './color.ts';

export class RGBAImage {
  width: number;
  height: number;
  data: Uint8ClampedArray;

  constructor(width: number, height: number, data?: Uint8ClampedArray) {
    this.width = width;
    this.height = height;
    this.data = data ?? new Uint8ClampedArray(width * height * 4);
  }

  static fromRows(rows: ReadonlyArray<ReadonlyArray<readonly [number, number, number, number]>>): RGBAImage {
    const height = rows.length;
    const width = rows[0]?.length ?? 0;
    const img = new RGBAImage(width, height);
    for (let y = 0; y < height; y++) {
      if (rows[y].length !== width) throw new Error('ragged rows');
      for (let x = 0; x < width; x++) {
        img.set(x, y, rows[y][x]);
      }
    }
    return img;
  }

  get(x: number, y: number): [number, number, number, number] {
    const o = (y * this.width + x) * 4;
    return [this.data[o], this.data[o + 1], this.data[o + 2], this.data[o + 3]];
  }

  set(x: number, y: number, px: readonly [number, number, number, number]): void {
    const o = (y * this.width + x) * 4;
    this.data[o] = px[0];
    this.data[o + 1] = px[1];
    this.data[o + 2] = px[2];
    this.data[o + 3] = px[3];
  }

  clone(): RGBAImage {
    return new RGBAImage(this.width, this.height, new Uint8ClampedArray(this.data));
  }
}

export class FloatImage {
  width: number;
  height: number;
  data: Float32Array;

  constructor(width: number, height: number, data?: Float32Array) {
    this.width = width;
    this.height = height;
    this.data = data ?? new Float32Array(width * height * 4);
  }

  static fromRGBA(img: RGBAImage): FloatImage {
    const out = new FloatImage(img.width, img.height);
    for (let i = 0; i < img.width * img.height; i++) {
      const a = img.data[i * 4 + 3] / 255;
      out.data[i * 4] = srgbByteToLinear(img.data[i * 4]) * a;
      out.data[i * 4 + 1] = srgbByteToLinear(img.data[i * 4 + 1]) * a;
      out.data[i * 4 + 2] = srgbByteToLinear(img.data[i * 4 + 2]) * a;
      out.data[i * 4 + 3] = a;
    }
    return out;
  }

  toRGBA(): RGBAImage {
    const out = new RGBAImage(this.width, this.height);
    for (let i = 0; i < this.width * this.height; i++) {
      const a = Math.min(1, Math.max(0, this.data[i * 4 + 3]));
      const un = a > 1e-12 ? 1 / a : 0;
      out.data[i * 4] = linearToSrgbByte(this.data[i * 4] * un);
      out.data[i * 4 + 1] = linearToSrgbByte(this.data[i * 4 + 1] * un);
      out.data[i * 4 + 2] = linearToSrgbByte(this.data[i * 4 + 2] * un);
      out.data[i * 4 + 3] = Math.round(a * 255);
    }
    return out;
  }

  get(x: number, y: number): [number, number, number, number] {
    const o = (y * this.width + x) * 4;
    return [this.data[o], this.data[o + 1], this.data[o + 2], this.data[o + 3]];
  }

  set(x: number, y: number, px: readonly [number, number, number, number]): void {
    const o = (y * this.width + x) * 4;
    this.data[o] = px[0];
    this.data[o + 1] = px[1];
    this.data[o + 2] = px[2];
    this.data[o + 3] = px[3];
  }

  clone(): FloatImage {
    return new FloatImage(this.width, this.height, new Float32Array(this.data));
  }
}
