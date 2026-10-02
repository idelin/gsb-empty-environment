/**
 * Minimal self-contained RGBA image. No Canvas, no image libraries — just a
 * Uint8ClampedArray and hand-rolled rasterization, used by the independent
 * verifier and by tests to build tiny fixtures.
 */
export class RGBAImage {
  readonly width: number;
  readonly height: number;
  readonly data: Uint8ClampedArray;

  constructor(width: number, height: number) {
    if (!Number.isInteger(width) || !Number.isInteger(height) || width <= 0 || height <= 0) {
      throw new Error('image dimensions must be positive integers');
    }
    this.width = width;
    this.height = height;
    this.data = new Uint8ClampedArray(width * height * 4);
  }

  private offset(x: number, y: number): number {
    return (y * this.width + x) * 4;
  }

  getPixel(x: number, y: number): [number, number, number, number] {
    if (x < 0 || y < 0 || x >= this.width || y >= this.height) {
      throw new Error(`pixel out of bounds: (${x}, ${y})`);
    }
    const o = this.offset(x, y);
    return [this.data[o]!, this.data[o + 1]!, this.data[o + 2]!, this.data[o + 3]!];
  }

  setPixel(x: number, y: number, rgba: [number, number, number, number]): void {
    if (x < 0 || y < 0 || x >= this.width || y >= this.height) {
      throw new Error(`pixel out of bounds: (${x}, ${y})`);
    }
    const o = this.offset(x, y);
    this.data[o] = rgba[0];
    this.data[o + 1] = rgba[1];
    this.data[o + 2] = rgba[2];
    this.data[o + 3] = rgba[3];
  }

  /**
   * Fill an integer rectangle. Returns the number of pixels that were already
   * non-zero (i.e. claimed by someone else) before this fill.
   */
  fillRect(
    x: number,
    y: number,
    w: number,
    h: number,
    rgba: [number, number, number, number],
  ): number {
    if (x < 0 || y < 0 || x + w > this.width || y + h > this.height || w < 0 || h < 0) {
      throw new Error(`rect out of bounds: (${x}, ${y}, ${w}, ${h})`);
    }
    let alreadyClaimed = 0;
    for (let yy = y; yy < y + h; yy++) {
      for (let xx = x; xx < x + w; xx++) {
        const o = this.offset(xx, yy);
        if (this.data[o + 3] !== 0) alreadyClaimed++;
        this.data[o] = rgba[0];
        this.data[o + 1] = rgba[1];
        this.data[o + 2] = rgba[2];
        this.data[o + 3] = rgba[3];
      }
    }
    return alreadyClaimed;
  }

  countClaimed(): number {
    let n = 0;
    for (let i = 3; i < this.data.length; i += 4) {
      if (this.data[i] !== 0) n++;
    }
    return n;
  }

  /** ASCII rendering for demos and debugging ('.' = empty). */
  toAscii(): string {
    const lines: string[] = [];
    for (let y = 0; y < this.height; y++) {
      let line = '';
      for (let x = 0; x < this.width; x++) {
        const o = this.offset(x, y);
        line += this.data[o + 3] === 0 ? '.' : String.fromCharCode(33 + (this.data[o]! % 90));
      }
      lines.push(line);
    }
    return lines.join('\n');
  }
}

/** Deterministic color per item id (FNV-1a hash). */
export function colorForId(id: string): [number, number, number, number] {
  let hash = 0x811c9dc5;
  for (let i = 0; i < id.length; i++) {
    hash ^= id.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return [
    64 + (hash & 0x7f),
    64 + ((hash >>> 8) & 0x7f),
    64 + ((hash >>> 16) & 0x7f),
    255,
  ];
}
