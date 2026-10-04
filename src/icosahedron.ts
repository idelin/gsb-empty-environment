/**
 * Icosahedron model: 12 vertices, 20 triangular faces (CCW seen from
 * outside), face adjacency, and - the key artefact - exact integer
 * affine "edge transforms" between the local IJ lattices of adjacent
 * faces.
 *
 * Geometry (vertices, normals, unfolding rotations) is computed once at
 * module initialisation using floating point; the resulting transforms
 * are rounded to exact integers and verified (involution, pure
 * 60-degree rotations, expected translations).  All runtime topology
 * traversal then uses only these integer tables.
 */

import {
  DIRECTIONS,
  applyMat,
  downAp7,
  ijEq,
  rotationPowers,
} from './hexmath.ts';
import type { IJ } from './hexmath.ts';

export interface Vec3 {
  x: number;
  y: number;
  z: number;
}

function v3(x: number, y: number, z: number): Vec3 {
  return { x, y, z };
}
function add(a: Vec3, b: Vec3): Vec3 {
  return v3(a.x + b.x, a.y + b.y, a.z + b.z);
}
function sub(a: Vec3, b: Vec3): Vec3 {
  return v3(a.x - b.x, a.y - b.y, a.z - b.z);
}
function scale(a: Vec3, s: number): Vec3 {
  return v3(a.x * s, a.y * s, a.z * s);
}
function dot(a: Vec3, b: Vec3): number {
  return a.x * b.x + a.y * b.y + a.z * b.z;
}
function cross(a: Vec3, b: Vec3): Vec3 {
  return v3(
    a.y * b.z - a.z * b.y,
    a.z * b.x - a.x * b.z,
    a.x * b.y - a.y * b.x,
  );
}
function len(a: Vec3): number {
  return Math.sqrt(dot(a, a));
}
function normalize(a: Vec3): Vec3 {
  return scale(a, 1 / len(a));
}

/** Rodrigues rotation of vector v around unit axis k by angle theta. */
function rotate(v: Vec3, k: Vec3, theta: number): Vec3 {
  const c = Math.cos(theta);
  const s = Math.sin(theta);
  const term1 = scale(v, c);
  const term2 = scale(cross(k, v), s);
  const term3 = scale(k, dot(k, v) * (1 - c));
  return add(add(term1, term2), term3);
}

export interface EdgeTransform {
  /** Adjacent face index across this edge. */
  toFace: number;
  /** Index of the shared edge on the adjacent face. */
  edgeOnTo: number;
  /** 2x2 rotation matrix (a power of the 60-degree rotation). */
  rot: [number, number, number, number];
  /** Integer translation (position of the neighbour face origin). */
  t: IJ;
}

/** Apply an edge transform to IJ coordinates: p' = rot * p + t. */
export function applyEdgeTransform(tr: EdgeTransform, p: IJ): IJ {
  const r = applyMat(tr.rot, p);
  return { i: r.i + tr.t.i, j: r.j + tr.t.j };
}

const PHI = (1 + Math.sqrt(5)) / 2;

function buildVertices(): Vec3[] {
  const raw: [number, number, number][] = [
    [0, 1, PHI],
    [0, 1, -PHI],
    [0, -1, PHI],
    [0, -1, -PHI],
    [1, PHI, 0],
    [1, -PHI, 0],
    [-1, PHI, 0],
    [-1, -PHI, 0],
    [PHI, 0, 1],
    [-PHI, 0, 1],
    [PHI, 0, -1],
    [-PHI, 0, -1],
  ];
  return raw.map(([x, y, z]) => normalize(v3(x, y, z)));
}

function buildFaces(verts: Vec3[]): number[][] {
  let dmin = Infinity;
  for (let a = 0; a < verts.length; a++) {
    for (let b = a + 1; b < verts.length; b++) {
      dmin = Math.min(dmin, len(sub(verts[a], verts[b])));
    }
  }
  const isEdge = (a: number, b: number) =>
    Math.abs(len(sub(verts[a], verts[b])) - dmin) < dmin * 1e-9;
  const faces: number[][] = [];
  for (let a = 0; a < verts.length; a++) {
    for (let b = a + 1; b < verts.length; b++) {
      if (!isEdge(a, b)) continue;
      for (let c = b + 1; c < verts.length; c++) {
        if (!isEdge(a, c) || !isEdge(b, c)) continue;
        const face = [a, b, c];
        const n = cross(
          sub(verts[b], verts[a]),
          sub(verts[c], verts[a]),
        );
        const centroid = scale(
          add(add(verts[a], verts[b]), verts[c]),
          1 / 3,
        );
        if (dot(n, centroid) < 0) {
          face[1] = c;
          face[2] = b;
        }
        faces.push(face);
      }
    }
  }
  if (faces.length !== 20) {
    throw new Error(`icosahedron: expected 20 faces, got ${faces.length}`);
  }
  return faces;
}

export class Icosahedron {
  readonly vertices: Vec3[];
  readonly faces: number[][];
  /** Unit vector towards the face centroid (for face lookup). */
  readonly centroidUnit: Vec3[];
  /** Actual 3D centroid (lies in the face plane). */
  readonly centroid3: Vec3[];
  /** Outward unit normal of each face. */
  readonly normals: Vec3[];
  /** IJ basis vector u (length = 2 * triangle inradius). */
  readonly basisU: Vec3[];
  /** IJ basis vector v (length = 2 * triangle inradius, 60 deg from u). */
  readonly basisV: Vec3[];
  /** transforms[face][edge] for edge = 0,1,2 (edge e opposite vertex e).
   *  Maps IJ coordinates of the NEIGHBOUR face into this face's frame. */
  readonly transforms: EdgeTransform[][];
  /** crossing[face][edge] maps this face's IJ coords into the neighbour's
   *  frame (the inverse direction of transforms). */
  readonly crossing: EdgeTransform[][];

  constructor() {
    this.vertices = buildVertices();
    this.faces = buildFaces(this.vertices);
    this.centroid3 = this.faces.map((f) =>
      scale(
        add(add(this.vertices[f[0]], this.vertices[f[1]]), this.vertices[f[2]]),
        1 / 3,
      ),
    );
    this.centroidUnit = this.centroid3.map((c) => normalize(c));
    this.normals = this.faces.map((f) => {
      const n = cross(
        sub(this.vertices[f[1]], this.vertices[f[0]]),
        sub(this.vertices[f[2]], this.vertices[f[0]]),
      );
      return normalize(n);
    });
    this.basisU = this.faces.map((f, idx) => {
      const mid = scale(add(this.vertices[f[1]], this.vertices[f[2]]), 0.5);
      return scale(sub(mid, this.centroid3[idx]), 2);
    });
    this.basisV = this.faces.map((f, idx) => {
      const mid = scale(add(this.vertices[f[0]], this.vertices[f[1]]), 0.5);
      return scale(sub(this.centroid3[idx], mid), 2);
    });
    this.transforms = this.buildTransforms();
    // crossing[f][e]: affine map from f's frame into the neighbour's frame
    // (borrowed from the reverse edge record) but tagged with the
    // neighbour's face index as the target.
    this.crossing = this.transforms.map((row, f) =>
      row.map((tr) => {
        const rev = this.transforms[tr.toFace][tr.edgeOnTo];
        return {
          toFace: tr.toFace,
          edgeOnTo: tr.edgeOnTo,
          rot: rev.rot,
          t: rev.t,
        };
      }),
    );
    this.verify();
  }

  /** Shared vertices of edge `edge` of face `face`. */
  edgeVertices(face: number, edge: number): [number, number] {
    const f = this.faces[face];
    return [f[(edge + 1) % 3], f[(edge + 2) % 3]];
  }

  /** Express 3D point P (in the face plane) as fractional IJ coords. */
  pointToIJ(face: number, p: Vec3): { alpha: number; beta: number } {
    const rel = sub(p, this.centroid3[face]);
    return this.vectorToIJ(face, rel);
  }

  /** Express a 3D in-plane vector as fractional IJ components. */
  vectorToIJ(face: number, rel: Vec3): { alpha: number; beta: number } {
    const u = this.basisU[face];
    const v = this.basisV[face];
    const uu = dot(u, u);
    const uv = dot(u, v);
    const vv = dot(v, v);
    const ru = dot(rel, u);
    const rv = dot(rel, v);
    const det = uu * vv - uv * uv;
    return {
      alpha: (ru * vv - rv * uv) / det,
      beta: (rv * uu - ru * uv) / det,
    };
  }

  /** 3D position of fractional IJ coords (alpha, beta) on a face. */
  ijToPoint(face: number, alpha: number, beta: number): Vec3 {
    return add(
      this.centroid3[face],
      add(scale(this.basisU[face], alpha), scale(this.basisV[face], beta)),
    );
  }

  private buildTransforms(): EdgeTransform[][] {
    // Map directed shared-vertex pair -> {face, edge}
    const edgeOwner = new Map<string, { face: number; edge: number }[]>();
    for (let f = 0; f < 20; f++) {
      for (let e = 0; e < 3; e++) {
        const [a, b] = this.edgeVertices(f, e);
        const key = a < b ? `${a}:${b}` : `${b}:${a}`;
        const list = edgeOwner.get(key) ?? [];
        list.push({ face: f, edge: e });
        edgeOwner.set(key, list);
      }
    }
    const expectedT: IJ[] = [DIRECTIONS[0], DIRECTIONS[2], DIRECTIONS[4]];
    const out: EdgeTransform[][] = [];
    for (let f = 0; f < 20; f++) {
      const row: EdgeTransform[] = [];
      for (let e = 0; e < 3; e++) {
        const [a, b] = this.edgeVertices(f, e);
        const key = a < b ? `${a}:${b}` : `${b}:${a}`;
        const owners = edgeOwner.get(key)!;
        if (owners.length !== 2) {
          throw new Error(`icosahedron: edge ${key} owned by ${owners.length}`);
        }
        const other = owners[0].face === f ? owners[1] : owners[0];
        const to = other.face;
        // Unfold face `to` onto the plane of face `f` by rotating around
        // the shared edge line (through vertices a and b).
        const axis = normalize(sub(this.vertices[b], this.vertices[a]));
        const angle = Math.acos(
          Math.max(-1, Math.min(1, dot(this.normals[to], this.normals[f]))),
        );
        let tr: EdgeTransform | null = null;
        for (const theta of [angle, -angle]) {
          const rotPoint = (p: Vec3) =>
            add(this.vertices[a], rotate(sub(p, this.vertices[a]), axis, theta));
          const rotVec = (p: Vec3) => rotate(p, axis, theta);
          const g = rotPoint(this.centroid3[to]);
          const u = rotVec(this.basisU[to]);
          const v = rotVec(this.basisV[to]);
          const tF = this.pointToIJ(f, g);
          const c1 = this.vectorToIJ(f, u);
          const c2 = this.vectorToIJ(f, v);
          const cand: EdgeTransform = {
            toFace: to,
            edgeOnTo: other.edge,
            rot: [
              Math.round(c1.alpha),
              Math.round(c2.alpha),
              Math.round(c1.beta),
              Math.round(c2.beta),
            ],
            t: { i: Math.round(tF.alpha), j: Math.round(tF.beta) },
          };
          const nearInt = (x: number) => Math.abs(x - Math.round(x)) < 1e-6;
          if (
            nearInt(tF.alpha) &&
            nearInt(tF.beta) &&
            nearInt(c1.alpha) &&
            nearInt(c1.beta) &&
            nearInt(c2.alpha) &&
            nearInt(c2.beta) &&
            ijEq(cand.t, expectedT[e])
          ) {
            tr = cand;
            break;
          }
        }
        if (!tr) {
          throw new Error(`icosahedron: cannot unfold face ${to} onto ${f}`);
        }
        row.push(tr);
      }
      out.push(row);
    }
    return out;
  }

  private verify(): void {
    const powers = rotationPowers();
    const isRot = (m: [number, number, number, number]) =>
      powers.some((p) => p[0] === m[0] && p[1] === m[1] && p[2] === m[2] && p[3] === m[3]);
    for (let f = 0; f < 20; f++) {
      for (let e = 0; e < 3; e++) {
        const tr = this.transforms[f][e];
        if (!isRot(tr.rot)) {
          throw new Error(`edge transform ${f}/${e} is not a 60-deg rotation`);
        }
        // Involution: crossing an edge twice must be the identity map.
        const back = this.transforms[tr.toFace][tr.edgeOnTo];
        if (back.toFace !== f || back.edgeOnTo !== e) {
          throw new Error(`edge transform ${f}/${e} not symmetric`);
        }
        const probe: IJ = { i: 3, j: -5 };
        const round = applyEdgeTransform(back, applyEdgeTransform(tr, probe));
        if (!ijEq(round, probe)) {
          throw new Error(`edge transform ${f}/${e} is not an involution`);
        }
      }
    }
  }
}

const crossingCache = new Map<number, EdgeTransform[][]>();

/**
 * Edge-crossing transforms valid for cells at resolution `res`.  The
 * rotation part is resolution-independent; the translation (neighbour
 * origin position) scales as M^res * t in the res-relative IJ lattice.
 */
export function crossingAt(res: number): EdgeTransform[][] {
  let cached = crossingCache.get(res);
  if (cached) return cached;
  const ico = getIcosahedron();
  cached = ico.crossing.map((row) =>
    row.map((tr) => {
      let t: IJ = { i: tr.t.i, j: tr.t.j };
      for (let r = 0; r < res; r++) t = downAp7(t, 0);
      return { toFace: tr.toFace, edgeOnTo: tr.edgeOnTo, rot: tr.rot, t };
    }),
  );
  crossingCache.set(res, cached);
  return cached;
}

let singleton: Icosahedron | null = null;

export function getIcosahedron(): Icosahedron {
  if (!singleton) singleton = new Icosahedron();
  return singleton;
}
