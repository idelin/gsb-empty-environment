import { RgbaImage } from './rgba-image.ts';
import { type ByteBlock, packBlock, expandBlock } from './rle.ts';

/**
 * A delta is the stored difference of one committed edit. It can be applied
 * forward (redo) and reverted (undo) against a live RgbaImage, and its stored
 * payload can be compressed in memory without changing semantics.
 */
export abstract class Delta {
  abstract apply(image: RgbaImage): void;
  abstract revert(image: RgbaImage): void;
  abstract compress(): void;
  abstract byteSize(): number;
}

/** Sparse pixel change set: only the pixels an edit actually touched. */
export class PixelDelta extends Delta {
  // pixel indices (y * width + x), ascending
  private indices: Uint32Array;
  // 4 bytes (RGBA) per index, before/after the edit
  private before: ByteBlock;
  private after: ByteBlock;

  constructor(indices: Uint32Array, before: Uint8Array, after: Uint8Array) {
    super();
    if (indices.length * 4 !== before.length || before.length !== after.length) {
      throw new Error('pixel delta payload mismatch');
    }
    this.indices = indices;
    this.before = { data: before, rle: false };
    this.after = { data: after, rle: false };
  }

  get count(): number {
    return this.indices.length;
  }

  apply(image: RgbaImage): void {
    this.write(image, expandBlock(this.after));
  }

  revert(image: RgbaImage): void {
    this.write(image, expandBlock(this.before));
  }

  private write(image: RgbaImage, bytes: Uint8Array): void {
    for (let i = 0; i < this.indices.length; i++) {
      const o = this.indices[i] * 4;
      image.data[o] = bytes[i * 4];
      image.data[o + 1] = bytes[i * 4 + 1];
      image.data[o + 2] = bytes[i * 4 + 2];
      image.data[o + 3] = bytes[i * 4 + 3];
    }
  }

  compress(): void {
    this.before = packBlock(expandBlock(this.before), true);
    this.after = packBlock(expandBlock(this.after), true);
  }

  byteSize(): number {
    return this.indices.byteLength + this.before.data.length + this.after.data.length;
  }
}

interface Strip {
  x: number;
  y: number;
  w: number;
  h: number;
  pixels: ByteBlock; // rows of w*4 bytes
}

/**
 * Metadata (dimension) change: crop from (beforeWidth x beforeHeight) to a
 * rect of (afterWidth x afterHeight) at (offsetX, offsetY). The removed
 * pixels are kept as strips so revert() restores them exactly.
 */
export class CropDelta extends Delta {
  beforeWidth: number;
  beforeHeight: number;
  afterWidth: number;
  afterHeight: number;
  offsetX: number;
  offsetY: number;
  private strips: Strip[];

  constructor(
    beforeWidth: number, beforeHeight: number,
    offsetX: number, offsetY: number,
    afterWidth: number, afterHeight: number,
    strips: Strip[],
  ) {
    super();
    this.beforeWidth = beforeWidth;
    this.beforeHeight = beforeHeight;
    this.offsetX = offsetX;
    this.offsetY = offsetY;
    this.afterWidth = afterWidth;
    this.afterHeight = afterHeight;
    this.strips = strips;
  }

  /** forward: re-crop the current image */
  apply(image: RgbaImage): void {
    const next = new Uint8ClampedArray(this.afterWidth * this.afterHeight * 4);
    for (let row = 0; row < this.afterHeight; row++) {
      const src = ((this.offsetY + row) * image.width + this.offsetX) * 4;
      next.set(image.data.subarray(src, src + this.afterWidth * 4), row * this.afterWidth * 4);
    }
    image.width = this.afterWidth;
    image.height = this.afterHeight;
    image.data = next;
  }

  /** inverse: restore original dimensions and the cropped-away pixels */
  revert(image: RgbaImage): void {
    const restored = new Uint8ClampedArray(this.beforeWidth * this.beforeHeight * 4);
    // paste the kept rect back at its offset
    for (let row = 0; row < this.afterHeight; row++) {
      const dst = ((this.offsetY + row) * this.beforeWidth + this.offsetX) * 4;
      restored.set(image.data.subarray(row * this.afterWidth * 4, (row + 1) * this.afterWidth * 4), dst);
    }
    // paste the removed strips
    for (const strip of this.strips) {
      const bytes = expandBlock(strip.pixels);
      for (let row = 0; row < strip.h; row++) {
        const dst = ((strip.y + row) * this.beforeWidth + strip.x) * 4;
        restored.set(bytes.subarray(row * strip.w * 4, (row + 1) * strip.w * 4), dst);
      }
    }
    image.width = this.beforeWidth;
    image.height = this.beforeHeight;
    image.data = restored;
  }

  compress(): void {
    for (const strip of this.strips) {
      strip.pixels = packBlock(expandBlock(strip.pixels), true);
    }
  }

  byteSize(): number {
    let total = 24; // six dimension/offset fields
    for (const strip of this.strips) total += 16 + strip.pixels.data.length;
    return total;
  }
}

/** A transaction: several deltas committed as one undoable history node. */
export class CompositeDelta extends Delta {
  private deltas: Delta[];

  constructor(deltas: Delta[]) {
    super();
    if (deltas.length === 0) throw new Error('empty transaction');
    this.deltas = deltas;
  }

  apply(image: RgbaImage): void {
    for (const d of this.deltas) d.apply(image);
  }

  revert(image: RgbaImage): void {
    for (let i = this.deltas.length - 1; i >= 0; i--) this.deltas[i].revert(image);
  }

  compress(): void {
    for (const d of this.deltas) d.compress();
  }

  byteSize(): number {
    return this.deltas.reduce((sum, d) => sum + d.byteSize(), 0);
  }
}

export function makeStrip(x: number, y: number, w: number, h: number, pixels: Uint8Array): Strip {
  return { x, y, w, h, pixels: { data: pixels, rle: false } };
}
