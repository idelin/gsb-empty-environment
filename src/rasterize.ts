/**
 * Rasterizer for possibly self-intersecting closed polylines.
 *
 * Sampling and boundary rules (explicit and fixed):
 *
 * 1. Sample points are pixel centers: pixel (i, j) is sampled at
 *    (originX + i + 1/2, originY + j + 1/2). The y axis points downward
 *    (image coordinates); this only flips the global winding sign and does
 *    not affect either fill rule's result.
 *
 * 2. A sample point lying exactly on the polygon boundary (on any edge,
 *    including horizontal edges, or exactly on a vertex) is classified as
 *    INSIDE under both fill rules ("boundary inclusive"). Exactness is
 *    decided with rational arithmetic, never with epsilon tolerances.
 *
 * 3. Otherwise a ray is cast from the sample point towards +x. A
 *    non-horizontal edge (a, b) crosses the scanline y = y0 iff
 *    min(a.y, b.y) <= y0 < max(a.y, b.y) (half-open: the lower-y endpoint
 *    is inclusive, the upper-y endpoint exclusive), so every vertex is
 *    accounted for exactly once and horizontal edges never count.
 *
 * 4. even-odd: inside iff the number of crossings strictly to the right of
 *    the sample point is odd. non-zero: inside iff the winding number
 *    (sum of +1 per upward edge and -1 per downward edge crossing strictly
 *    to the right) is non-zero.
 *
 * All intersection points are computed as exact rationals (BigInt
 * fractions), so ties between an intersection and a pixel center are
 * detected exactly.
 */

import { Rat } from './rational';

export interface Point {
  readonly x: number;
  readonly y: number;
}

export type PointLike = Point | readonly [number, number];

export interface RasterizeOptions {
  /** Viewport width in pixels (positive integer). */
  readonly width: number;
  /** Viewport height in pixels (positive integer). */
  readonly height: number;
  /** X coordinate of the viewport's left edge. Default 0. */
  readonly originX?: number;
  /** Y coordinate of the viewport's top edge. Default 0. */
  readonly originY?: number;
}

/** A pixel mask in row-major order; 1 = inside, 0 = outside. */
export class Mask {
  constructor(
    readonly width: number,
    readonly height: number,
    readonly data: Uint8Array,
  ) {}

  at(x: number, y: number): number {
    return this.data[y * this.width + x]!;
  }

  /** Render as text rows ('#' inside, '.' outside), for debugging. */
  toString(): string {
    const rows: string[] = [];
    for (let y = 0; y < this.height; y++) {
      let row = '';
      for (let x = 0; x < this.width; x++) {
        row += this.at(x, y) ? '#' : '.';
      }
      rows.push(row);
    }
    return rows.join('\n');
  }
}

export interface RasterizeResult {
  readonly width: number;
  readonly height: number;
  /** Mask under the even-odd rule. */
  readonly evenOdd: Mask;
  /** Mask under the non-zero winding rule. */
  readonly nonZero: Mask;
  /** Mask of pixels where the two rules disagree (evenOdd XOR nonZero). */
  readonly diff: Mask;
  /** Pixel coordinates [x, y] where the two rules disagree. */
  diffPixels(): Array<[number, number]>;
}

interface Vertex {
  readonly x: Rat;
  readonly y: Rat;
}

interface Edge {
  readonly a: Vertex;
  readonly b: Vertex;
}

interface Crossing {
  readonly x: Rat;
  readonly delta: 1 | -1;
}

function normalizePoint(p: PointLike): [number, number] {
  if (Array.isArray(p)) {
    const arr = p as readonly number[];
    return [arr[0]!, arr[1]!];
  }
  return [(p as Point).x, (p as Point).y];
}

/**
 * Rasterize a closed polyline. The polyline is closed implicitly (an edge
 * from the last vertex back to the first is assumed); repeating the first
 * vertex at the end is allowed. Zero-length edges are ignored.
 */
export function rasterize(
  points: readonly PointLike[],
  options: RasterizeOptions,
): RasterizeResult {
  const { width, height } = options;
  if (!Number.isInteger(width) || width <= 0) {
    throw new Error(`rasterize: width must be a positive integer, got ${width}`);
  }
  if (!Number.isInteger(height) || height <= 0) {
    throw new Error(`rasterize: height must be a positive integer, got ${height}`);
  }
  if (points.length < 3) {
    throw new Error(`rasterize: need at least 3 vertices, got ${points.length}`);
  }

  const originX = Rat.fromNumber(options.originX ?? 0);
  const originY = Rat.fromNumber(options.originY ?? 0);

  const vertices: Vertex[] = points.map((p) => {
    const [x, y] = normalizePoint(p);
    return { x: Rat.fromNumber(x), y: Rat.fromNumber(y) };
  });

  const edges: Edge[] = [];
  for (let i = 0; i < vertices.length; i++) {
    const a = vertices[i]!;
    const b = vertices[(i + 1) % vertices.length]!;
    if (a.x.eq(b.x) && a.y.eq(b.y)) continue; // zero-length edge
    edges.push({ a, b });
  }

  const evenOddData = new Uint8Array(width * height);
  const nonZeroData = new Uint8Array(width * height);
  const diffData = new Uint8Array(width * height);
  const widthBig = BigInt(width);

  for (let row = 0; row < height; row++) {
    const yc = originY.add(Rat.of(BigInt(row))).add(Rat.HALF);
    const crossings: Crossing[] = [];
    const boundary = new Uint8Array(width);

    for (const { a, b } of edges) {
      const yCmp = a.y.cmp(b.y);
      if (yCmp === 0) {
        // Horizontal edge: never a crossing, but pixel centers lying on it
        // are boundary points.
        if (a.y.eq(yc)) {
          const lo = a.x.cmp(b.x) <= 0 ? a.x : b.x;
          const hi = a.x.cmp(b.x) <= 0 ? b.x : a.x;
          for (let i = 0; i < width; i++) {
            const xc = originX.add(Rat.of(BigInt(i))).add(Rat.HALF);
            if (lo.cmp(xc) <= 0 && xc.cmp(hi) <= 0) boundary[i] = 1;
          }
        }
        continue;
      }
      const lo = yCmp < 0 ? a.y : b.y;
      const hi = yCmp < 0 ? b.y : a.y;
      if (lo.cmp(yc) > 0 || yc.cmp(hi) > 0) continue; // scanline misses edge
      // Exact x where the edge meets the scanline:
      // x = a.x + (yc - a.y) * (b.x - a.x) / (b.y - a.y)
      const t = yc.sub(a.y).div(b.y.sub(a.y));
      const xInt = a.x.add(t.mul(b.x.sub(a.x)));

      // Boundary: the edge passes exactly through a pixel center on this row.
      const idx = xInt.sub(originX).sub(Rat.HALF);
      if (idx.isInteger() && idx.num >= 0n && idx.num < widthBig) {
        boundary[Number(idx.num)] = 1;
      }

      // Half-open rule: include the scanline of the lower-y endpoint only.
      if (yc.cmp(hi) < 0) {
        crossings.push({ x: xInt, delta: yCmp < 0 ? 1 : -1 });
      }
    }

    crossings.sort((p, q) => p.x.cmp(q.x));

    let idx = 0;
    let countRight = crossings.length;
    let wnRight = 0;
    for (const c of crossings) wnRight += c.delta;

    const rowBase = row * width;
    let xc = originX.add(Rat.HALF);
    for (let i = 0; i < width; i++) {
      // Consume crossings strictly left of the sample point.
      while (idx < crossings.length && crossings[idx]!.x.cmp(xc) < 0) {
        countRight--;
        wnRight -= crossings[idx]!.delta;
        idx++;
      }
      // Crossings exactly at the sample point: the point is on the
      // boundary; exclude them from the right-hand side and mark inside.
      let onBoundary = boundary[i] === 1;
      while (idx < crossings.length && crossings[idx]!.x.cmp(xc) === 0) {
        countRight--;
        wnRight -= crossings[idx]!.delta;
        idx++;
        onBoundary = true;
      }

      let eo: number;
      let nz: number;
      if (onBoundary) {
        eo = 1;
        nz = 1;
      } else {
        eo = countRight & 1;
        nz = wnRight !== 0 ? 1 : 0;
      }
      evenOddData[rowBase + i] = eo;
      nonZeroData[rowBase + i] = nz;
      diffData[rowBase + i] = eo ^ nz;

      xc = xc.add(Rat.ONE);
    }
  }

  const evenOdd = new Mask(width, height, evenOddData);
  const nonZero = new Mask(width, height, nonZeroData);
  const diff = new Mask(width, height, diffData);

  return {
    width,
    height,
    evenOdd,
    nonZero,
    diff,
    diffPixels() {
      const out: Array<[number, number]> = [];
      for (let y = 0; y < height; y++) {
        for (let x = 0; x < width; x++) {
          if (diff.at(x, y)) out.push([x, y]);
        }
      }
      return out;
    },
  };
}
