/**
 * Exact Euclidean (squared) distance transform on a 2D obstacle grid.
 *
 * Uses the separable two-pass algorithm of Felzenszwalb & Huttenlocher
 * ("Distance Transforms of Sampled Functions", 2012), which is *exact*
 * for the squared Euclidean metric (no chamfer approximation).
 *
 * Every output cell additionally records the coordinates of its nearest
 * obstacle ("source"). Ties between equidistant obstacles are broken by
 * source coordinates: smaller x wins, then smaller y. This rule is applied
 * identically by the batch transform and the incremental repair, so both
 * always agree.
 */

export const INF = Number.POSITIVE_INFINITY;

export interface EDTResult {
  width: number;
  height: number;
  /** Squared Euclidean distance to the nearest obstacle. Infinity when none. */
  dist: Float64Array;
  /** Nearest obstacle source x per cell (-1 when no obstacle exists). */
  srcX: Int32Array;
  /** Nearest obstacle source y per cell (-1 when no obstacle exists). */
  srcY: Int32Array;
}

export interface Grid {
  width: number;
  height: number;
  /** True where the cell is an obstacle. */
  obstacle: (x: number, y: number) => boolean;
}

/** Compare two candidate sources for a cell: distance first, then (x, y). */
export function betterSource(
  dA: number, ax: number, ay: number,
  dB: number, bx: number, by: number,
): boolean {
  // returns true when source A is strictly better than source B
  if (dA !== dB) return dA < dB;
  if (ax !== bx) return ax < bx;
  return ay < by;
}

/**
 * Compute the exact squared-distance field of `grid`.
 * Cells outside [0,width) x [0,height) are never produced; callers map
 * world coordinates (possibly negative) into this local frame themselves.
 */
export function computeEDT(grid: Grid): EDTResult {
  const { width: w, height: h } = grid;
  const n = w * h;
  const dist = new Float64Array(n);
  const srcX = new Int32Array(n);
  const srcY = new Int32Array(n);

  if (w === 0 || h === 0) return { width: w, height: h, dist, srcX, srcY };

  // ---- Pass 1: 1-D exact transform along each column -------------------
  // For every column x, nearest obstacle (in y) per row, with tie-break on
  // source y (source x is constant within a column).
  const colDist = new Float64Array(h);
  const colSrcY = new Int32Array(h);
  const f = new Float64Array(h); // distance of each row to its column source
  const v = new Int32Array(h);   // locations of parabola sources
  const z = new Float64Array(h + 1); // intersection boundaries

  for (let x = 0; x < w; x++) {
    let m = 0; // number of obstacles in this column
    for (let y = 0; y < h; y++) {
      if (grid.obstacle(x, y)) {
        f[m] = 0;
        v[m] = y;
        m++;
      }
    }
    if (m === 0) {
      for (let y = 0; y < h; y++) {
        colDist[y] = INF;
        colSrcY[y] = -1;
      }
    } else {
      // Build lower envelope of parabolas rooted at obstacle rows.
      let k = 0;
      const pv = new Int32Array(m);
      const pz = new Float64Array(m + 1);
      pv[0] = 0;
      pz[0] = -INF;
      pz[1] = INF;
      for (let q = 1; q < m; q++) {
        let s: number;
        for (;;) {
          const p = pv[k];
          // intersection of parabola p and parabola q (roots v[p], v[q])
          s = ((f[q] + v[q] * v[q]) - (f[p] + v[p] * v[p])) / (2 * v[q] - 2 * v[p]);
          if (s > pz[k]) break;
          k--;
        }
        k++;
        pv[k] = q;
        pz[k] = s;
        pz[k + 1] = INF;
      }
      k = 0;
      for (let y = 0; y < h; y++) {
        while (pz[k + 1] < y) k++;
        const p = pv[k];
        const dy = y - v[p];
        colDist[y] = dy * dy;
        colSrcY[y] = v[p];
      }
    }
    for (let y = 0; y < h; y++) {
      const i = y * w + x;
      dist[i] = colDist[y];
      srcX[i] = colSrcY[y] < 0 ? -1 : x;
      srcY[i] = colSrcY[y];
    }
  }

  // ---- Pass 2: 1-D transform along each row ----------------------------
  // Parabola q rooted at column v[q] with height f[q]; ties inside the
  // envelope are resolved with the full (dist, srcX, srcY) ordering.
  const rowF = new Float64Array(w);
  const rowV = new Int32Array(w);
  const rowSrcY = new Int32Array(w);
  const pv = new Int32Array(w);
  const pz = new Float64Array(w + 1);

  for (let y = 0; y < h; y++) {
    const base = y * w;
    let m = 0;
    for (let x = 0; x < w; x++) {
      const i = base + x;
      if (srcX[i] >= 0) {
        rowF[m] = dist[i];
        rowV[m] = x;
        rowSrcY[m] = srcY[i];
        m++;
      }
    }
    if (m === 0) {
      for (let x = 0; x < w; x++) {
        const i = base + x;
        dist[i] = INF;
        srcX[i] = -1;
        srcY[i] = -1;
      }
      continue;
    }
    let k = 0;
    pv[0] = 0;
    pz[0] = -INF;
    pz[1] = INF;
    for (let q = 1; q < m; q++) {
      for (;;) {
        const p = pv[k];
        if (rowV[q] === rowV[p]) {
          // Same root column: keep the better candidate outright.
          if (
            betterSource(rowF[q], rowV[q], rowSrcY[q], rowF[p], rowV[p], rowSrcY[p])
          ) {
            pv[k] = q;
          }
          break; // q is merged; do not push a new parabola
        }
        const s =
          ((rowF[q] + rowV[q] * rowV[q]) - (rowF[p] + rowV[p] * rowV[p])) /
          (2 * rowV[q] - 2 * rowV[p]);
        if (s > pz[k]) {
          k++;
          pv[k] = q;
          pz[k] = s;
          pz[k + 1] = INF;
          break;
        }
        if (k === 0) {
          // q fully dominates p on the whole row: replace it.
          pv[0] = q;
          pz[0] = -INF;
          pz[1] = INF;
          break;
        }
        k--;
      }
    }
    k = 0;
    for (let x = 0; x < w; x++) {
      while (pz[k + 1] < x) k++;
      const p = pv[k];
      const dx = x - rowV[p];
      const d = rowF[p] + dx * dx;
      const i = base + x;
      // The envelope gives the minimal distance; the stored source of the
      // winning parabola is the tie-broken representative. When several
      // parabolas yield the same distance at x, the envelope boundary s
      // places x on exactly one of them; equal-distance boundaries keep the
      // smaller-x source because s is an integer midpoint rounded toward
      // the left parabola, and left sources have smaller x.
      dist[i] = d;
      srcX[i] = rowV[p];
      srcY[i] = rowSrcY[p];
    }
  }

  return { width: w, height: h, dist, srcX, srcY };
}
