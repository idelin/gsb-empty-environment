/** Minimal self-contained RGBA8 image; no canvas, no image libraries. */
export class RGBAImage {
  readonly width: number;
  readonly height: number;
  readonly data: Uint8ClampedArray;

  constructor(width: number, height: number, data?: Uint8ClampedArray) {
    this.width = width;
    this.height = height;
    this.data = data ?? new Uint8ClampedArray(width * height * 4);
  }

  static filled(width: number, height: number, r: number, g: number, b: number, a: number): RGBAImage {
    const img = new RGBAImage(width, height);
    for (let i = 0; i < img.data.length; i += 4) {
      img.data[i] = r;
      img.data[i + 1] = g;
      img.data[i + 2] = b;
      img.data[i + 3] = a;
    }
    return img;
  }

  get(px: number, py: number): [number, number, number, number] {
    const i = (py * this.width + px) * 4;
    return [this.data[i], this.data[i + 1], this.data[i + 2], this.data[i + 3]];
  }

  set(px: number, py: number, r: number, g: number, b: number, a: number): void {
    const i = (py * this.width + px) * 4;
    this.data[i] = r;
    this.data[i + 1] = g;
    this.data[i + 2] = b;
    this.data[i + 3] = a;
  }

  equals(other: RGBAImage): boolean {
    if (this.width !== other.width || this.height !== other.height) return false;
    for (let i = 0; i < this.data.length; i++) {
      if (this.data[i] !== other.data[i]) return false;
    }
    return true;
  }

  diffCount(other: RGBAImage): number {
    let n = 0;
    for (let i = 0; i < this.data.length; i++) {
      if (this.data[i] !== other.data[i]) n++;
    }
    return n;
  }
}
