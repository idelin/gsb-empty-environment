/**
 * Pure-integer hex-lattice mathematics (IJ coordinate space).
 *
 * The lattice uses axial-like integer coordinates (i, j) with the hex
 * metric  d(i, j) = i*i + i*j + j*j.  The 6 unit directions are the
 * neighbours of the origin.  No floating point and no trigonometry is
 * used anywhere in this module: hierarchy traversal (aperture-7) and
 * neighbourhood walks are exact integer computations.
 */

export interface IJ {
  i: number;
  j: number;
}

export function ij(i: number, j: number): IJ {
  return { i, j };
}

export function ijAdd(a: IJ, b: IJ): IJ {
  return { i: a.i + b.i, j: a.j + b.j };
}

export function ijSub(a: IJ, b: IJ): IJ {
  return { i: a.i - b.i, j: a.j - b.j };
}

export function ijEq(a: IJ, b: IJ): boolean {
  return a.i === b.i && a.j === b.j;
}

/** Hex norm i^2 + i*j + j^2 (squared hex distance from origin). */
export function ijNorm(a: IJ): number {
  return a.i * a.i + a.i * a.j + a.j * a.j;
}

/** The 6 unit directions, counter-clockwise starting at (1, 0). */
export const DIRECTIONS: readonly IJ[] = Object.freeze([
  { i: 1, j: 0 },
  { i: 0, j: 1 },
  { i: -1, j: 1 },
  { i: -1, j: 0 },
  { i: 0, j: -1 },
  { i: 1, j: -1 },
]);

/**
 * Child offsets for the aperture-7 hierarchy: digit 0 is the centre
 * child, digits 1..6 are the 6 neighbours (matching DIRECTIONS).
 */
export const CHILD_OFFSETS: readonly IJ[] = Object.freeze([
  { i: 0, j: 0 },
  ...DIRECTIONS,
]);

/**
 * Aperture-7 refinement matrix M = [[2, -1], [1, 3]].
 * det(M) = 7 and both columns have hex norm 7, so M scales the lattice
 * by sqrt(7) and rotates it; the 7 child offsets form a complete residue
 * system of Z^2 / M Z^2, i.e. the children exactly tile the parent.
 */
export function downAp7(parent: IJ, digit: number): IJ {
  const off = CHILD_OFFSETS[digit];
  return {
    i: 2 * parent.i - parent.j + off.i,
    j: parent.i + 3 * parent.j + off.j,
  };
}

export interface UpResult {
  parent: IJ;
  digit: number;
}

/**
 * Exact integer inverse of {@link downAp7}: returns the parent cell and
 * the child digit.  M^-1 = (1/7) * [[3, 1], [-1, 2]]; the nearest parent
 * lattice point is found by testing the 4 floor/ceil candidates around
 * the rational solution with the hex metric, ties broken
 * deterministically (smallest i, then j) so the map is translation
 * equivariant and the digit decomposition is unique.
 */
export function upAp7(cell: IJ): UpResult {
  const ai = 3 * cell.i + cell.j; // 7 * fractional parent i
  const aj = -cell.i + 2 * cell.j; // 7 * fractional parent j
  const fi = Math.floor(ai / 7);
  const fj = Math.floor(aj / 7);
  let best: IJ = { i: 0, j: 0 };
  let bestDist = Number.MAX_SAFE_INTEGER;
  let first = true;
  for (const ci of [fi, fi + 1]) {
    for (const cj of [fj, fj + 1]) {
      const di = cell.i - (2 * ci - cj);
      const dj = cell.j - (ci + 3 * cj);
      const d = di * di + di * dj + dj * dj;
      if (
        first ||
        d < bestDist ||
        (d === bestDist && (ci < best.i || (ci === best.i && cj < best.j)))
      ) {
        first = false;
        bestDist = d;
        best = { i: ci, j: cj };
      }
    }
  }
  const ri = cell.i - (2 * best.i - best.j);
  const rj = cell.j - (best.i + 3 * best.j);
  let digit = -1;
  for (let k = 0; k < CHILD_OFFSETS.length; k++) {
    if (CHILD_OFFSETS[k].i === ri && CHILD_OFFSETS[k].j === rj) {
      digit = k;
      break;
    }
  }
  if (digit < 0) {
    throw new Error(`upAp7: remainder (${ri},${rj}) is not a child offset`);
  }
  return { parent: best, digit };
}

/** 60-degree counter-clockwise rotation in IJ space: (i, j) -> (-j, i+j). */
export const ROT60: readonly [number, number, number, number] = Object.freeze([
  0, -1, 1, 1,
]) as [number, number, number, number];

export function rot60(p: IJ): IJ {
  return { i: -p.j, j: p.i + p.j };
}

/** All 6 powers of the 60-degree rotation (including identity). */
export function rotationPowers(): [number, number, number, number][] {
  const out: [number, number, number, number][] = [];
  let r: [number, number, number, number] = [1, 0, 0, 1];
  for (let k = 0; k < 6; k++) {
    out.push(r);
    r = [
      ROT60[0] * r[0] + ROT60[1] * r[2],
      ROT60[0] * r[1] + ROT60[1] * r[3],
      ROT60[2] * r[0] + ROT60[3] * r[2],
      ROT60[2] * r[1] + ROT60[3] * r[3],
    ];
  }
  return out;
}

export function applyMat(m: readonly [number, number, number, number], p: IJ): IJ {
  return { i: m[0] * p.i + m[1] * p.j, j: m[2] * p.i + m[3] * p.j };
}

/** Cube-coordinate rounding: nearest lattice point to fractional (x, y). */
export function hexRound(x: number, y: number): IJ {
  const z = -x - y;
  let rx = Math.round(x);
  let ry = Math.round(y);
  let rz = Math.round(z);
  const dx = Math.abs(rx - x);
  const dy = Math.abs(ry - y);
  const dz = Math.abs(rz - z);
  if (dx > dy && dx > dz) {
    rx = -ry - rz;
  } else if (dy > dz) {
    ry = -rx - rz;
  }
  return { i: rx, j: ry };
}
