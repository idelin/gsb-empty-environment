/**
 * Core data types shared by the palette builder, diffuser, streamer
 * and revision routines.
 */

/** A raw RGBA image. `data` is `width * height * 4` bytes in row-major order. */
export interface RgbaImage {
  width: number;
  height: number;
  data: Uint8Array | Uint8ClampedArray;
}

/** An opaque RGB triple (channels are integers in [0, 255]). */
export type Rgb = readonly [number, number, number];

/** Result of choosing a fixed-size palette from a set of opaque colors. */
export interface PaletteResult {
  /**
   * Exactly `colorCount` entries, sorted by a deterministic key. Every entry
   * is an opaque color; transparent pixels never consume a palette slot.
   * When the source contains fewer distinct colors, entries are deterministically
   * duplicated so that the palette always has the requested length.
   */
  palette: Rgb[];
  /** Sum of squared RGB error over opaque source pixels using nearest-palette assignment. */
  sse: number;
  /** "exact" when a provably minimal SSE was computed, otherwise the heuristic name. */
  method: "exact" | "median-cut";
  /**
   * True only when `sse` is certified to be the global minimum. A heuristic
   * result is never certified optimal.
   */
  certifiedBest: boolean;
  /**
   * Relative gap to an exact baseline: (heuristicSSE - exactSSE) / exactSSE
   * when both are computable, otherwise `null`. The implementation never
   * claims optimality for a heuristic; it reports this measured gap instead.
   */
  gapVsExact: number | null;
  /** Exact baseline SSE when the domain was small enough to solve exactly. */
  exactSse: number | null;
  /** Distinct opaque colors that were seen in the source image. */
  distinctOpaqueColors: number;
  /** Deterministic fingerprint of `palette`. */
  fingerprint: string;
}

/** A quantized indexed image. Index 0..colorCount-1 are opaque colors. */
export interface IndexedImage {
  width: number;
  height: number;
  /** Opaque palette entries, length `colorCount`. */
  palette: Rgb[];
  /** Index per pixel; `transparentIndex` marks transparent pixels. */
  indices: Uint16Array;
  /** Sentinel index used for transparent pixels (`palette.length`). */
  transparentIndex: number;
}

/** Method used to assign opaque pixels to palette entries. */
export type QuantMethod = "none" | "floyd-steinberg";

/** Versioned, serializable diffusion frontier (cross-row error state). */
export interface FrontierState {
  /** Color-space / algorithm version, used to reject incompatible restores. */
  colorSpaceVersion: string;
  width: number;
  height: number;
  /** Number of rows already produced. */
  row: number;
  /** Indices already produced (length width*height; rows >= row are zero). */
  indices: Uint16Array;
  /** Scaled residual already produced (length width*height*3). */
  residual: Int32Array;
  /** Scaled incoming vertical error per column (7/16 units are merged on read). */
  down: Int32Array;
  /** Scaled diagonal error carried into the next row (3/16 and 5/16 parts). */
  downLeft: Int32Array;
  downRight: Int32Array;
  method: QuantMethod;
}

/** A single local pixel edit (RGBA replacement). */
export interface PixelEdit {
  x: number;
  y: number;
  rgba: RgbaPixel;
}

export type RgbaPixel = readonly [number, number, number, number];

/** Result of revising pixels on an already diffused image. */
export interface RevisionResult {
  indices: Uint16Array;
  /** Per-pixel scaled residual after the revision (same shape as the image). */
  residual: Int32Array;
  /** Number of pixels whose index actually changed. */
  changedPixels: number;
  /** Pixel coordinates (y * width + x) that had to be recomputed. */
  affectedPixels: number;
}
