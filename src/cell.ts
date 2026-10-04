/**
 * Hierarchical hexagonal cell identifiers.
 *
 * Bit layout of the 64-bit cell id (rendered as a 16-char hex string):
 *   bits 60..63  mode (1 = cell)
 *   bits 56..59  resolution (0..15)
 *   bits 51..55  icosahedron face (0..19)
 *   bits  0..50  17 aperture-7 digit slots (3 bits each, slot 0 at the top);
 *                the first `res` slots hold digits 0..6, unused slots are 7.
 *
 * Resolutions up to 15 are supported.
 */

import {
  CHILD_OFFSETS,
  DIRECTIONS,
  downAp7,
  hexRound,
  upAp7,
} from './hexmath.ts';
import type { IJ } from './hexmath.ts';
import {
  applyEdgeTransform,
  crossingAt,
  getIcosahedron,
} from './icosahedron.ts';
import type { EdgeTransform } from './icosahedron.ts';

export const MAX_RESOLUTION = 15;
export const NUM_FACES = 20;

const MODE_CELL = 1n;
const DIGIT_SLOTS = 17;
const UNUSED_DIGIT = 7n;
const HEX_RE = /^[0-9a-fA-F]{16}$/;

export class CellError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CellError';
  }
}

export interface FaceIJ {
  face: number;
  ij: IJ;
}

/* ------------------------------------------------------------------ */
/* digit <-> IJ conversion (pure integer)                              */
/* ------------------------------------------------------------------ */

export function digitsToIj(digits: readonly number[]): IJ {
  let cur: IJ = { i: 0, j: 0 };
  for (const d of digits) {
    if (d < 0 || d > 6) throw new CellError(`invalid digit ${d}`);
    cur = downAp7(cur, d);
  }
  return cur;
}

/**
 * Decompose IJ coordinates into `res` aperture-7 digits.  Throws if the
 * point does not belong to the face region (its res-level ancestor is
 * not the face origin).
 */
export function ijToDigits(ij: IJ, res: number): number[] {
  const digits = new Array<number>(res);
  let cur = ij;
  for (let r = res - 1; r >= 0; r--) {
    const up = upAp7(cur);
    digits[r] = up.digit;
    cur = up.parent;
  }
  if (cur.i !== 0 || cur.j !== 0) {
    throw new CellError('IJ point is outside the face region');
  }
  return digits;
}

/** True iff the res-level ancestor of `ij` is the face origin. */
export function isContained(ij: IJ, res: number): boolean {
  let cur = ij;
  for (let r = 0; r < res; r++) {
    cur = upAp7(cur).parent;
  }
  return cur.i === 0 && cur.j === 0;
}

/* ------------------------------------------------------------------ */
/* id packing / unpacking                                              */
/* ------------------------------------------------------------------ */

export function packCellId(res: number, face: number, digits: readonly number[]): bigint {
  if (!Number.isInteger(res) || res < 0 || res > MAX_RESOLUTION) {
    throw new CellError(`resolution out of range: ${res}`);
  }
  if (!Number.isInteger(face) || face < 0 || face >= NUM_FACES) {
    throw new CellError(`face out of range: ${face}`);
  }
  if (digits.length !== res) {
    throw new CellError(`expected ${res} digits, got ${digits.length}`);
  }
  let id =
    (MODE_CELL << 60n) |
    (BigInt(res) << 56n) |
    (BigInt(face) << 51n);
  for (let slot = 0; slot < DIGIT_SLOTS; slot++) {
    const d = slot < digits.length ? BigInt(digits[slot]) : UNUSED_DIGIT;
    id |= d << BigInt((DIGIT_SLOTS - 1 - slot) * 3);
  }
  return id;
}

export interface UnpackedCell {
  res: number;
  face: number;
  digits: number[];
}

export function unpackCellId(id: bigint): UnpackedCell {
  const mode = (id >> 60n) & 0xfn;
  if (mode !== MODE_CELL) throw new CellError(`invalid mode ${mode}`);
  const res = Number((id >> 56n) & 0xfn);
  if (res > MAX_RESOLUTION) throw new CellError(`invalid resolution ${res}`);
  const face = Number((id >> 51n) & 0x1fn);
  if (face >= NUM_FACES) throw new CellError(`invalid face ${face}`);
  const digits: number[] = [];
  for (let slot = 0; slot < DIGIT_SLOTS; slot++) {
    const d = Number((id >> BigInt((DIGIT_SLOTS - 1 - slot) * 3)) & 7n);
    if (slot < res) {
      if (d > 6) throw new CellError(`invalid digit ${d} at slot ${slot}`);
      digits.push(d);
    } else if (d !== 7) {
      throw new CellError(`unused slot ${slot} must be 7, got ${d}`);
    }
  }
  return { res, face, digits };
}

export function cellIdToHex(id: bigint): string {
  return id.toString(16).padStart(16, '0');
}

export function hexToCellId(hex: string): bigint {
  if (typeof hex !== 'string' || !HEX_RE.test(hex)) {
    throw new CellError(`invalid cell hex string: ${JSON.stringify(hex)}`);
  }
  return BigInt(`0x${hex}`);
}

/* ------------------------------------------------------------------ */
/* public cell API                                                     */
/* ------------------------------------------------------------------ */

export function isValidCell(cell: string): boolean {
  try {
    unpackCellId(hexToCellId(cell));
    return true;
  } catch {
    return false;
  }
}

export function getResolution(cell: string): number {
  return unpackCellId(hexToCellId(cell)).res;
}

export function cellToFaceIJ(cell: string): FaceIJ & { res: number } {
  const { res, face, digits } = unpackCellId(hexToCellId(cell));
  return { face, ij: digitsToIj(digits), res };
}

export function cellFromFaceIJ(face: number, ij: IJ, res: number): string {
  const digits = ijToDigits(ij, res);
  return cellIdToHex(packCellId(res, face, digits));
}

/**
 * Resolve an IJ position that may lie outside a face's region into the
 * canonical (face, ij) of the cell covering it, crossing one icosahedron
 * edge or, near vertices, two edges.  Returns null for positions that
 * fall into the 60-degree wedge gap at an icosahedron vertex (the
 * pentagon defect of the tiling).  Pure integer computation.
 */
export function resolveFaceIJ(face: number, ij: IJ, res: number): FaceIJ | null {
  if (isContained(ij, res)) return { face, ij };
  const crossing = crossingAt(res);
  const direct: EdgeTransform[] = crossing[face];
  for (const tr of direct) {
    const mapped = applyEdgeTransform(tr, ij);
    if (isContained(mapped, res)) return { face: tr.toFace, ij: mapped };
  }
  for (const tr1 of direct) {
    const hop1 = applyEdgeTransform(tr1, ij);
    for (const tr2 of crossing[tr1.toFace]) {
      if (tr2.toFace === face) continue;
      const hop2 = applyEdgeTransform(tr2, hop1);
      if (isContained(hop2, res)) return { face: tr2.toFace, ij: hop2 };
    }
  }
  return null;
}


/**
 * Points inside the 60-degree defect wedge at an icosahedron vertex are
 * not covered by any cell.  Walk from the rounded lattice point towards
 * the face origin (greedy hex steps) until a resolvable cell is found;
 * this yields the (approximately) nearest valid cell to the coordinate.
 */
function nearestResolvable(face: number, start: IJ, res: number): FaceIJ | null {
  let cur = start;
  for (let steps = 0; steps < 10_000_000; steps++) {
    const resolved = resolveFaceIJ(face, cur, res);
    if (resolved) return resolved;
    let bestDir: IJ = DIRECTIONS[0];
    let bestNorm = Infinity;
    for (const d of DIRECTIONS) {
      const nxt = { i: cur.i + d.i, j: cur.j + d.j };
      const n = nxt.i * nxt.i + nxt.i * nxt.j + nxt.j * nxt.j;
      if (n < bestNorm) {
        bestNorm = n;
        bestDir = d;
      }
    }
    cur = { i: cur.i + bestDir.i, j: cur.j + bestDir.j };
  }
  return null;
}

/* ------------------------------------------------------------------ */
/* geographic encoding (floating point confined to this boundary)      */
/* ------------------------------------------------------------------ */

export interface LatLng {
  lat: number;
  lng: number;
}

function assertLatLng(lat: number, lng: number): void {
  if (
    !Number.isFinite(lat) ||
    !Number.isFinite(lng) ||
    lat < -90 ||
    lat > 90 ||
    lng < -180 ||
    lng > 180
  ) {
    throw new CellError(`invalid coordinate (${lat}, ${lng})`);
  }
}

/** Encode a geographic coordinate as a cell id hex string. */
export function latLngToCell(lat: number, lng: number, res: number): string {
  assertLatLng(lat, lng);
  if (!Number.isInteger(res) || res < 0 || res > MAX_RESOLUTION) {
    throw new CellError(`resolution out of range: ${res}`);
  }
  const ico = getIcosahedron();
  const phi = (lat * Math.PI) / 180;
  const lam = (lng * Math.PI) / 180;
  const p = {
    x: Math.cos(phi) * Math.cos(lam),
    y: Math.cos(phi) * Math.sin(lam),
    z: Math.sin(phi),
  };
  let face = 0;
  let bestDot = -Infinity;
  for (let f = 0; f < NUM_FACES; f++) {
    const c = ico.centroidUnit[f];
    const d = p.x * c.x + p.y * c.y + p.z * c.z;
    if (d > bestDot) {
      bestDot = d;
      face = f;
    }
  }
  // Ray from the sphere centre through p, intersected with the face plane.
  const n = ico.normals[face];
  const g = ico.centroid3[face];
  const t =
    (g.x * n.x + g.y * n.y + g.z * n.z) / (p.x * n.x + p.y * n.y + p.z * n.z);
  const plane = { x: p.x * t, y: p.y * t, z: p.z * t };
  const { alpha, beta } = ico.pointToIJ(face, plane);
  const scaleFactor = Math.pow(7, res / 2);
  const ij = hexRound(alpha * scaleFactor, beta * scaleFactor);
  const resolved = nearestResolvable(face, ij, res);
  if (!resolved) {
    throw new CellError(
      'coordinate falls into an icosahedron vertex defect gap',
    );
  }
  return cellFromFaceIJ(resolved.face, resolved.ij, res);
}

/** Geographic coordinate of the cell centre. */
export function cellToLatLng(cell: string): LatLng {
  const { face, ij, res } = cellToFaceIJ(cell);
  const ico = getIcosahedron();
  const scaleFactor = Math.pow(7, res / 2);
  const p = ico.ijToPoint(face, ij.i / scaleFactor, ij.j / scaleFactor);
  const len = Math.sqrt(p.x * p.x + p.y * p.y + p.z * p.z);
  const lat = (Math.asin(p.z / len) * 180) / Math.PI;
  const lng = (Math.atan2(p.y, p.x) * 180) / Math.PI;
  return { lat, lng };
}

/* ------------------------------------------------------------------ */
/* hierarchy                                                           */
/* ------------------------------------------------------------------ */

export function cellToParent(cell: string, parentRes?: number): string {
  const { res, face, digits } = unpackCellId(hexToCellId(cell));
  const target = parentRes ?? res - 1;
  if (!Number.isInteger(target) || target < 0 || target >= res) {
    throw new CellError(`invalid parent resolution ${String(parentRes)}`);
  }
  return cellIdToHex(packCellId(target, face, digits.slice(0, target)));
}

export function cellToChildren(cell: string, childRes?: number): string[] {
  const { res, face, digits } = unpackCellId(hexToCellId(cell));
  const target = childRes ?? res + 1;
  if (!Number.isInteger(target) || target <= res || target > MAX_RESOLUTION) {
    throw new CellError(`invalid child resolution ${String(childRes)}`);
  }
  let frontier: number[][] = [digits];
  for (let r = res; r < target; r++) {
    const next: number[][] = [];
    for (const prefix of frontier) {
      for (let d = 0; d < CHILD_OFFSETS.length; d++) {
        next.push([...prefix, d]);
      }
    }
    frontier = next;
  }
  return frontier.map((d) => cellIdToHex(packCellId(target, face, d)));
}

/** Cell centre of the icosahedron face, useful for tests and demos. */
export function faceCenterCell(face: number, res: number): string {
  return cellIdToHex(packCellId(res, face, new Array(res).fill(0)));
}
