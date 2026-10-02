/** Per-side gap edge (spacing) around a texture, counted toward page capacity. */
export interface Margin {
  top: number;
  right: number;
  bottom: number;
  left: number;
}

/** Margin shorthand: uniform number or partial per-side object (missing sides = 0). */
export type MarginSpec = number | Partial<Margin>;

/** Where a fixed (immovable) texture already sits. */
export interface FixedPlacement {
  page: number;
  x: number;
  y: number;
  rotated?: boolean;
}

/** Input description of one texture to pack. */
export interface ItemSpec {
  id: string;
  /** Texture width/height in pixels (before rotation). */
  w: number;
  h: number;
  /** Gap edge around the texture; consumes capacity on the page. Default 0. */
  margin?: MarginSpec;
  /** Whether 90-degree rotation is allowed. Default true. */
  rotatable?: boolean;
  /** If set, the texture is already placed and must never move. */
  fixed?: FixedPlacement;
}

/** One placed texture in the output layout. */
export interface Placement {
  id: string;
  page: number;
  /** Texture origin on the page (margin excluded). */
  x: number;
  y: number;
  /** Stored size on the page (swapped vs. source when rotated). */
  w: number;
  h: number;
  rotated: boolean;
  margin: Margin;
}

export type UnpackedReason = 'too-large' | 'no-space' | 'invalid';

export interface UnpackedItem {
  id: string;
  reason: UnpackedReason;
  detail: string;
}

/** A full multi-page layout. */
export interface Layout {
  pageW: number;
  pageH: number;
  pages: number;
  placements: Placement[];
  unpacked: UnpackedItem[];
}

export interface PackOptions {
  pageW: number;
  pageH: number;
  /**
   * Exact solver is used to prove optimality when the number of movable
   * items is at most this. Default 12. Set 0 to disable.
   */
  exactThreshold?: number;
}

export type BoundCertificate = 'exact' | 'heuristic';

export interface PackBounds {
  /** Provable lower bound on the number of pages. */
  lower: number;
  /** Achieved upper bound (pages used by the returned layout). */
  upper: number;
  /** Proven optimum when certificate === 'exact'. */
  optimal: number | null;
  certificate: BoundCertificate;
}

export interface PackResult {
  layout: Layout;
  bounds: PackBounds;
}

export function normalizeMargin(spec: MarginSpec | undefined): Margin {
  if (spec === undefined) return { top: 0, right: 0, bottom: 0, left: 0 };
  if (typeof spec === 'number') {
    return { top: spec, right: spec, bottom: spec, left: spec };
  }
  return {
    top: spec.top ?? 0,
    right: spec.right ?? 0,
    bottom: spec.bottom ?? 0,
    left: spec.left ?? 0,
  };
}

export function isNonNegInt(n: number): boolean {
  return Number.isInteger(n) && n >= 0;
}

export function isPosInt(n: number): boolean {
  return Number.isInteger(n) && n > 0;
}

/** Footprint = texture rect inflated by its margin. This is what consumes capacity. */
export interface Footprint {
  x: number;
  y: number;
  w: number;
  h: number;
}

export function footprintOf(p: Placement): Footprint {
  return {
    x: p.x - p.margin.left,
    y: p.y - p.margin.top,
    w: p.w + p.margin.left + p.margin.right,
    h: p.h + p.margin.top + p.margin.bottom,
  };
}

export function footprintsOverlap(a: Footprint, b: Footprint): boolean {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}
