import { clampChannel, nearestIndex } from "./color.ts";
import {
  canDeliver,
  FS_SCALE,
  W_DOWN,
  W_DOWN_LEFT,
  W_DOWN_RIGHT,
  W_RIGHT,
  type MaskGrid,
} from "./diffusion-core.ts";
import { COLOR_SPACE_VERSION, opacityMask } from "./image.ts";
import type {
  FrontierState,
  IndexedImage,
  QuantMethod,
  Rgb,
  RgbaImage,
} from "./types.ts";

export interface EncodeOptions {
  method?: QuantMethod;
}

/** Scaled per-channel residuals for one pixel: [errR, errG, errB] * 16. */
export type PixelResidual = readonly [number, number, number];

export interface FullIndexedImage extends IndexedImage {
  /**
   * Scaled quantization residual per pixel, interleaved RGB (length n * 3).
   * For Floyd-Steinberg values are 16x the (corrected - chosen) difference.
   * Used by the local-revision routine to recompute only the diffusion cone.
   */
  residual: Int32Array;
}

export function residualAt(residual: Int32Array, pixelIndex: number): PixelResidual {
  const o = pixelIndex * 3;
  return [residual[o]!, residual[o + 1]!, residual[o + 2]!];
}

/**
 * Quantize an RGBA image to a fixed-size opaque palette plus a transparent
 * sentinel index.
 *
 * Palette selection is deliberately NOT part of this routine: the caller
 * supplies `palette` (see buildPalette). This keeps palette choice and
 * in-barrier error diffusion strictly separated.
 *
 * With Floyd-Steinberg, transparent pixels are a hard barrier: they carry no
 * residual and error is never delivered through them (see canDeliver).
 */
export function encodeIndexed(
  image: RgbaImage,
  palette: readonly Rgb[],
  options: EncodeOptions = {}
): FullIndexedImage {
  const method: QuantMethod = options.method ?? "floyd-steinberg";
  const { width, height } = image;
  const n = width * height;
  const opaque = opacityMask(image);
  const grid: MaskGrid = { width, height, opaque };

  const indices = new Uint16Array(n);
  const residual = new Int32Array(n * 3);
  const transparentIndex = palette.length;

  if (method === "none") {
    for (let i = 0; i < n; i++) {
      const o = i * 4;
      if (!opaque[i]) {
        indices[i] = transparentIndex;
        continue;
      }
      const idx = nearestIndex(image.data[o]!, image.data[o + 1]!, image.data[o + 2]!, palette);
      indices[i] = idx;
      const p = palette[idx]!;
      residual[i * 3] = image.data[o]! - p[0];
      residual[i * 3 + 1] = image.data[o + 1]! - p[1];
      residual[i * 3 + 2] = image.data[o + 2]! - p[2];
    }
    return { width, height, palette: [...palette], indices, transparentIndex, residual };
  }

  const cols = {
    down: [new Int32Array(width), new Int32Array(width), new Int32Array(width)],
    downLeft: [new Int32Array(width), new Int32Array(width), new Int32Array(width)],
    downRight: [new Int32Array(width), new Int32Array(width), new Int32Array(width)],
  };
  encodeRows(grid, image, palette, indices, residual, 0, height, cols);

  return { width, height, palette: [...palette], indices, transparentIndex, residual };
}

interface RowState {
  down: Int32Array[];
  downLeft: Int32Array[];
  downRight: Int32Array[];
}

export function encodeRows(
  grid: MaskGrid,
  image: RgbaImage,
  palette: readonly Rgb[],
  indices: Uint16Array,
  residual: Int32Array,
  startRow: number,
  endRow: number,
  cols: { down: Int32Array[]; downLeft: Int32Array[]; downRight: Int32Array[] }
): RowState {
  const { width } = grid;
  const transparentIndex = palette.length;
  // scratch[0..2] collect error pushed from row y to row y+1 while row y runs.
  const scratch = () => [
    new Int32Array(width),
    new Int32Array(width),
    new Int32Array(width),
  ] as Int32Array[];

  for (let y = startRow; y < endRow; y++) {
    let carry: [number, number, number] = [0, 0, 0];
    const nextDown = scratch();
    const nextDownLeft = scratch();
    const nextDownRight = scratch();

    for (let x = 0; x < width; x++) {
      const i = y * width + x;
      const o = i * 4;
      if (!grid.opaque[i]) {
        indices[i] = transparentIndex;
        carry = [0, 0, 0];
        continue;
      }

      const corrected: [number, number, number] = [0, 0, 0];
      for (let c = 0; c < 3; c++) {
        const incoming =
          carry[c]! + cols.down[c]![x]! + cols.downLeft[c]![x]! + cols.downRight[c]![x]!;
        corrected[c] = clampChannel(image.data[o + c]! + Math.floor(incoming / FS_SCALE));
      }
      carry = [0, 0, 0];

      const idx = nearestIndex(corrected[0], corrected[1], corrected[2], palette);
      const chosen = palette[idx]!;
      indices[i] = idx;

      const errs: [number, number, number] = [
        corrected[0] - chosen[0],
        corrected[1] - chosen[1],
        corrected[2] - chosen[2],
      ];
      residual[i * 3] = errs[0];
      residual[i * 3 + 1] = errs[1];
      residual[i * 3 + 2] = errs[2];

      if (canDeliver(grid, x, y, x + 1, y)) {
        for (let c = 0; c < 3; c++) carry[c]! += errs[c]! * W_RIGHT;
      }
      if (canDeliver(grid, x, y, x - 1, y + 1)) {
        for (let c = 0; c < 3; c++) nextDownLeft[c]![x - 1] += errs[c]! * W_DOWN_LEFT;
      }
      if (canDeliver(grid, x, y, x, y + 1)) {
        for (let c = 0; c < 3; c++) nextDown[c]![x] += errs[c]! * W_DOWN;
      }
      if (canDeliver(grid, x, y, x + 1, y + 1)) {
        for (let c = 0; c < 3; c++) nextDownRight[c]![x + 1] += errs[c]! * W_DOWN_RIGHT;
      }
    }

    cols.down = nextDown;
    cols.downLeft = nextDownLeft;
    cols.downRight = nextDownRight;
  }

  return { down: cols.down, downLeft: cols.downLeft, downRight: cols.downRight };
}

/**
 * Streaming, chunk-friendly encoder. Producing rows in any sequence of chunk
 * sizes yields byte-identical indices/residuals to a one-shot encode, because
 * cross-row state lives solely in the versioned frontier.
 */
export class DiffusionEncoder {
  private readonly image: RgbaImage;
  private readonly palette: readonly Rgb[];
  private readonly method: QuantMethod;
  private readonly grid: MaskGrid;
  private readonly indices: Uint16Array;
  private readonly residual: Int32Array;
  private readonly down: Int32Array[];
  private readonly downLeft: Int32Array[];
  private readonly downRight: Int32Array[];
  private row = 0;
  readonly transparentIndex: number;

  constructor(image: RgbaImage, palette: readonly Rgb[], options: EncodeOptions = {}) {
    this.image = image;
    this.palette = palette;
    this.method = options.method ?? "floyd-steinberg";
    this.grid = { width: image.width, height: image.height, opaque: opacityMask(image) };
    this.indices = new Uint16Array(image.width * image.height);
    this.residual = new Int32Array(image.width * image.height * 3);
    const make = () => [
      new Int32Array(image.width),
      new Int32Array(image.width),
      new Int32Array(image.width),
    ];
    this.down = make();
    this.downLeft = make();
    this.downRight = make();
    this.transparentIndex = palette.length;
  }

  get width(): number {
    return this.image.width;
  }

  get height(): number {
    return this.image.height;
  }

  get currentRow(): number {
    return this.row;
  }

  get done(): boolean {
    return this.row >= this.image.height;
  }

  /** Produce the next `rowCount` rows (fewer at the end). */
  processRows(rowCount: number): {
    rows: number;
    indices: Uint16Array;
    residual: Int32Array;
    startRow: number;
  } {
    const startRow = this.row;
    const end = Math.min(
      this.image.height,
      startRow + Math.max(0, rowCount)
    );

    if (this.method === "none") {
      for (let y = startRow; y < end; y++) {
        for (let x = 0; x < this.image.width; x++) {
          const i = y * this.image.width + x;
          const o = i * 4;
          if (!this.grid.opaque[i]) {
            this.indices[i] = this.transparentIndex;
          } else {
            this.indices[i] = nearestIndex(
              this.image.data[o]!,
              this.image.data[o + 1]!,
              this.image.data[o + 2]!,
              this.palette
            );
          }
        }
      }
    } else {
      const cols = {
        down: this.down,
        downLeft: this.downLeft,
        downRight: this.downRight,
      };
      encodeRows(this.grid, this.image, this.palette, this.indices, this.residual, startRow, end, cols);
      this.down = cols.down;
      this.downLeft = cols.downLeft;
      this.downRight = cols.downRight;
    }

    this.row = end;
    const start = startRow * this.image.width;
    const stop = end * this.image.width;
    return {
      rows: end - startRow,
      startRow,
      indices: this.indices.subarray(start, stop),
      residual: this.residual.subarray(start * 3, stop * 3),
    };
  }

  saveFrontier(): FrontierState {
    return {
      colorSpaceVersion: COLOR_SPACE_VERSION,
      width: this.image.width,
      height: this.image.height,
      row: this.row,
      indices: Uint16Array.from(this.indices),
      residual: Int32Array.from(this.residual),
      down: this.snapshotCols(this.down),
      downLeft: this.snapshotCols(this.downLeft),
      downRight: this.snapshotCols(this.downRight),
      method: this.method,
    };
  }

  private snapshotCols(cols: Int32Array[]): Int32Array {
    // Interleave 3 channels for a single typed array in the saved state.
    const w = this.image.width;
    const out = new Int32Array(w * 3);
    for (let x = 0; x < w; x++) {
      out[x * 3] = cols[0]![x]!;
      out[x * 3 + 1] = cols[1]![x]!;
      out[x * 3 + 2] = cols[2]![x]!;
    }
    return out;
  }

  toJSON(): string {
    return JSON.stringify(this.saveFrontier(), (_key, v) => {
      if (v instanceof Int32Array) return { __int32: Array.from(v) };
      if (v instanceof Uint16Array) return { __uint16: Array.from(v) };
      return v;
    });
  }

  static restore(
    image: RgbaImage,
    palette: readonly Rgb[],
    state: FrontierState,
    options: EncodeOptions = {}
  ): DiffusionEncoder {
    const method: QuantMethod = options.method ?? state.method;
    if (state.colorSpaceVersion !== COLOR_SPACE_VERSION) {
      throw new Error(
        `incompatible color space version: saved=${state.colorSpaceVersion} current=${COLOR_SPACE_VERSION}`
      );
    }
    if (state.width !== image.width || state.height !== image.height) {
      throw new Error("frontier dimensions do not match image");
    }
    if (state.method !== method) {
      throw new Error(`frontier method mismatch: saved=${state.method} requested=${method}`);
    }
    const enc = new DiffusionEncoder(image, palette, { method });
    enc.row = state.row;
    if (state.indices) enc.indices.set(state.indices);
    if (state.residual) enc.residual.set(state.residual);
    enc.restoreCols(enc.down, state.down);
    enc.restoreCols(enc.downLeft, state.downLeft);
    enc.restoreCols(enc.downRight, state.downRight);
    return enc;
  }

  private restoreCols(cols: Int32Array[], flat: Int32Array): void {
    const w = this.image.width;
    for (let x = 0; x < w; x++) {
      cols[0]![x] = flat[x * 3]!;
      cols[1]![x] = flat[x * 3 + 1]!;
      cols[2]![x] = flat[x * 3 + 2]!;
    }
  }

  result(): FullIndexedImage {
    return {
      width: this.image.width,
      height: this.image.height,
      palette: [...this.palette],
      indices: this.indices,
      transparentIndex: this.transparentIndex,
      residual: this.residual,
    };
  }
}

export function frontierFromJSON(json: string): FrontierState {
  const parsed = JSON.parse(json) as {
    colorSpaceVersion: string;
    width: number;
    height: number;
    row: number;
    down: { __int32: number[] } | number[];
    downLeft: { __int32: number[] } | number[];
    downRight: { __int32: number[] } | number[];
    indices?: { __uint16: number[] } | number[];
    residual?: { __int32: number[] } | number[];
    method: QuantMethod;
  };
  const toI32 = (v: { __int32: number[] } | number[]): Int32Array =>
    Int32Array.from("__int32" in v ? v.__int32 : v);
  return {
    colorSpaceVersion: parsed.colorSpaceVersion,
    width: parsed.width,
    height: parsed.height,
    row: parsed.row,
    down: toI32(parsed.down),
    downLeft: toI32(parsed.downLeft),
    downRight: toI32(parsed.downRight),
    indices: parsed.indices
      ? Uint16Array.from("__uint16" in parsed.indices ? parsed.indices.__uint16 : (parsed.indices as number[]))
      : new Uint16Array(parsed.width * parsed.height),
    residual: parsed.residual ? toI32(parsed.residual) : new Int32Array(parsed.width * parsed.height * 3),
    method: parsed.method,
  };
}
