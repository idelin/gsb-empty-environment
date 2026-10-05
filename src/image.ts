export class RgbaImage {
  readonly width: number;
  readonly height: number;
  readonly data: Uint8ClampedArray;

  constructor(width: number, height: number, data?: Uint8ClampedArray) {
    this.width = width;
    this.height = height;
    this.data = data ?? new Uint8ClampedArray(width * height * 4);
    if (this.data.length !== width * height * 4) {
      throw new Error('RGBA data length mismatch');
    }
  }

  static fromAscii(rows: string[], palette: Record<string, [number, number, number, number]>): RgbaImage {
    const height = rows.length;
    const width = rows[0]?.length ?? 0;
    const img = new RgbaImage(width, height);
    for (let y = 0; y < height; y++) {
      if (rows[y].length !== width) throw new Error('ragged ascii rows');
      for (let x = 0; x < width; x++) {
        const px = palette[rows[y][x]];
        if (!px) throw new Error(`no palette entry for '${rows[y][x]}'`);
        img.set(x, y, px);
      }
    }
    return img;
  }

  get(x: number, y: number): [number, number, number, number] {
    const i = (y * this.width + x) * 4;
    return [this.data[i], this.data[i + 1], this.data[i + 2], this.data[i + 3]];
  }

  set(x: number, y: number, px: [number, number, number, number]): void {
    const i = (y * this.width + x) * 4;
    this.data[i] = px[0];
    this.data[i + 1] = px[1];
    this.data[i + 2] = px[2];
    this.data[i + 3] = px[3];
  }

  /** Integer luminance; gray-valued pixels (r=g=b) round-trip exactly. */
  toGray(): GrayImage {
    const out = new GrayImage(this.width, this.height);
    for (let i = 0, j = 0; i < this.data.length; i += 4, j++) {
      const r = this.data[i];
      const g = this.data[i + 1];
      const b = this.data[i + 2];
      out.data[j] = r === g && g === b ? r : ((r * 299 + g * 587 + b * 114 + 500) / 1000) | 0;
    }
    return out;
  }
}

export class GrayImage {
  readonly width: number;
  readonly height: number;
  readonly data: Uint8Array;

  constructor(width: number, height: number, data?: Uint8Array) {
    this.width = width;
    this.height = height;
    this.data = data ?? new Uint8Array(width * height);
    if (this.data.length !== width * height) {
      throw new Error('gray data length mismatch');
    }
  }

  static filled(width: number, height: number, value: number): GrayImage {
    const img = new GrayImage(width, height);
    img.data.fill(value);
    return img;
  }

  static fromAscii(rows: string[], palette: Record<string, number>): GrayImage {
    const height = rows.length;
    const width = rows[0]?.length ?? 0;
    const img = new GrayImage(width, height);
    for (let y = 0; y < height; y++) {
      if (rows[y].length !== width) throw new Error('ragged ascii rows');
      for (let x = 0; x < width; x++) {
        const v = palette[rows[y][x]];
        if (v === undefined) throw new Error(`no palette entry for '${rows[y][x]}'`);
        img.data[y * width + x] = v;
      }
    }
    return img;
  }

  get(x: number, y: number): number {
    return this.data[y * this.width + x];
  }

  set(x: number, y: number, v: number): void {
    this.data[y * this.width + x] = (v < 0 ? 0 : v > 255 ? 255 : v) & 0xff;
  }

  clone(): GrayImage {
    return new GrayImage(this.width, this.height, new Uint8Array(this.data));
  }

  equals(other: GrayImage): boolean {
    if (this.width !== other.width || this.height !== other.height) return false;
    for (let i = 0; i < this.data.length; i++) {
      if (this.data[i] !== other.data[i]) return false;
    }
    return true;
  }

  toRgba(): RgbaImage {
    const out = new RgbaImage(this.width, this.height);
    for (let i = 0, j = 0; j < this.data.length; i += 4, j++) {
      const v = this.data[j];
      out.data[i] = v;
      out.data[i + 1] = v;
      out.data[i + 2] = v;
      out.data[i + 3] = 255;
    }
    return out;
  }

  toAscii(ramp = ' .:-=+*#%@'): string[] {
    const rows: string[] = [];
    for (let y = 0; y < this.height; y++) {
      let row = '';
      for (let x = 0; x < this.width; x++) {
        const v = this.get(x, y);
        row += ramp[Math.min(ramp.length - 1, (v * ramp.length) / 256 | 0)];
      }
      rows.push(row);
    }
    return rows;
  }
}
