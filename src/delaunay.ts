import { orient2d, inCircle, inCircleBound, circumcenter } from "./geometry.ts";
import type { Point } from "./geometry.ts";

export interface MeshTriangle {
  /** Indices (into Mesh.points) of the three vertices, counter-clockwise. */
  vertices: [number, number, number];
  /**
   * Adjacency: neighbors[i] is the index of the triangle sharing the edge
   * opposite vertices[i], or -1 when that edge lies on the convex hull.
   */
  neighbors: [number, number, number];
  circumcenter: Point;
  circumradius: number;
}

export interface TriangleMesh {
  points: Point[];
  triangles: MeshTriangle[];
  /** Outer boundary: vertex indices in CCW order. */
  hull: number[];
}

interface Tri {
  v: [number, number, number];
  n: [number, number, number];
  alive: boolean;
}

const SUPER_COUNT = 3;

/**
 * Incremental Delaunay triangulator.
 *
 * Points are inserted one at a time into a bounding super-triangle.
 * Each insertion splits the containing triangle into 3 (or 4 when the
 * point lands on an edge), then Lawson's flip algorithm restores the
 * empty-circumcircle property locally via an edge stack.
 */
export class Delaunay {
  private pts: Point[] = []; // [super0, super1, super2, ...userPoints]
  private tris: Tri[] = [];
  private userPts: Point[] = [];
  private lastTri = 0;
  private scale = 1;

  constructor(points: Point[] = []) {
    for (const p of points) this.insert(p);
  }

  get size(): number {
    return this.userPts.length;
  }

  /** Insert a point; returns its user index (existing index if duplicate). */
  insert(p: Point): number {
    if (this.tris.length === 0) {
      this.userPts.push(p);
      this.rebuild();
      return this.userPts.length - 1;
    }
    if (!this.insideSuper(p)) {
      this.userPts.push(p);
      this.rebuild();
      return this.userPts.length - 1;
    }
    const idx = this.insertInternal(p);
    if (idx === this.userPts.length) this.userPts.push(p);
    return idx;
  }

  /** Build the compact output mesh: triangles, adjacency, convex hull. */
  mesh(): TriangleMesh {
    const idMap = new Map<number, number>();
    const kept: Tri[] = [];
    for (let i = 0; i < this.tris.length; i++) {
      const t = this.tris[i];
      if (!t.alive) continue;
      if (t.v[0] < SUPER_COUNT || t.v[1] < SUPER_COUNT || t.v[2] < SUPER_COUNT) continue;
      idMap.set(i, kept.length);
      kept.push(t);
    }

    const boundaryEdges: Array<[number, number]> = [];
    const triangles: MeshTriangle[] = kept.map((t) => {
      const vertices = t.v.map((v) => v - SUPER_COUNT) as [number, number, number];
      const neighbors = t.n.map((nid, ei) => {
        const mapped = nid >= 0 ? idMap.get(nid) : undefined;
        if (mapped === undefined) {
          boundaryEdges.push([vertices[(ei + 1) % 3], vertices[(ei + 2) % 3]]);
          return -1;
        }
        return mapped;
      }) as [number, number, number];
      const [a, b, c] = vertices.map((vi) => this.userPts[vi]);
      const cc = circumcenter(a, b, c);
      return {
        vertices,
        neighbors,
        circumcenter: cc,
        circumradius: Math.hypot(a.x - cc.x, a.y - cc.y),
      };
    });

    return {
      points: this.userPts.map((p) => ({ ...p })),
      triangles,
      hull: this.walkHull(boundaryEdges),
    };
  }

  // ---------------------------------------------------------------- internal

  private rebuild(): void {
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    for (const p of this.userPts) {
      if (p.x < minX) minX = p.x;
      if (p.y < minY) minY = p.y;
      if (p.x > maxX) maxX = p.x;
      if (p.y > maxY) maxY = p.y;
    }
    const cx = (minX + maxX) / 2;
    const cy = (minY + maxY) / 2;
    const span = Math.max(maxX - minX, maxY - minY, 1);
    // Grow the super-triangle factor until it contains every user point.
    let k = 16;
    for (;;) {
      const R = span * k;
      this.pts = [
        { x: cx - 2 * R, y: cy - R },
        { x: cx + 2 * R, y: cy - R },
        { x: cx, y: cy + 2 * R },
      ];
      if (this.userPts.every((p) => this.insideSuper(p))) {
        this.scale = R;
        break;
      }
      k *= 4;
    }
    this.tris = [{ v: [0, 1, 2], n: [-1, -1, -1], alive: true }];
    this.lastTri = 0;
    const all = [...this.userPts];
    this.userPts = [];
    for (const p of all) {
      this.insertInternal(p);
      this.userPts.push(p);
    }
  }

  private eps(): number {
    return 1e-12 * this.scale * this.scale;
  }

  private insideSuper(p: Point): boolean {
    const [s0, s1, s2] = this.pts;
    return (
      orient2d(s0, s1, p) >= 0 &&
      orient2d(s1, s2, p) >= 0 &&
      orient2d(s2, s0, p) >= 0
    );
  }

  private insertInternal(p: Point): number {
    const t = this.locate(p);
    const tri = this.tris[t];
    const eps = this.eps();

    for (let i = 0; i < 3; i++) {
      const vi = tri.v[i];
      if (vi >= SUPER_COUNT) {
        const q = this.pts[vi];
        const dx = p.x - q.x;
        const dy = p.y - q.y;
        if (dx * dx + dy * dy <= eps * eps) return vi - SUPER_COUNT;
      }
    }

    const pi = this.pts.length;
    this.pts.push(p);

    // Point exactly on a shared edge -> split the two adjacent triangles into 4.
    for (let ei = 0; ei < 3; ei++) {
      const a = this.pts[tri.v[(ei + 1) % 3]];
      const b = this.pts[tri.v[(ei + 2) % 3]];
      if (Math.abs(orient2d(a, b, p)) <= eps && tri.n[ei] >= 0 && this.tris[tri.n[ei]].alive) {
        this.split4(t, ei, pi);
        this.lastTri = t;
        return pi - SUPER_COUNT;
      }
    }

    this.split3(t, pi);
    this.lastTri = t;
    return pi - SUPER_COUNT;
  }

  private locate(p: Point): number {
    const eps = this.eps();
    let t = this.tris[this.lastTri]?.alive ? this.lastTri : this.firstAlive();
    for (let guard = 0; guard < 10000; guard++) {
      const tri = this.tris[t];
      let stepped = false;
      for (let i = 0; i < 3; i++) {
        const a = this.pts[tri.v[(i + 1) % 3]];
        const b = this.pts[tri.v[(i + 2) % 3]];
        if (orient2d(a, b, p) < -eps) {
          const nt = tri.n[i];
          if (nt >= 0 && this.tris[nt].alive) {
            t = nt;
            stepped = true;
          }
          break;
        }
      }
      if (!stepped) {
        if (this.contains(t, p, eps)) {
          this.lastTri = t;
          return t;
        }
        break; // fall through to robust linear scan
      }
    }
    for (let k = 0; k < this.tris.length; k++) {
      if (this.tris[k].alive && this.contains(k, p, eps)) {
        this.lastTri = k;
        return k;
      }
    }
    return t;
  }

  private contains(t: number, p: Point, eps: number): boolean {
    const tri = this.tris[t];
    for (let i = 0; i < 3; i++) {
      const a = this.pts[tri.v[(i + 1) % 3]];
      const b = this.pts[tri.v[(i + 2) % 3]];
      if (orient2d(a, b, p) < -eps) return false;
    }
    return true;
  }

  private firstAlive(): number {
    for (let i = 0; i < this.tris.length; i++) {
      if (this.tris[i].alive) return i;
    }
    return 0;
  }

  private replaceNeighbor(nid: number, oldId: number, newId: number): void {
    if (nid < 0) return;
    const tri = this.tris[nid];
    if (!tri.alive) return;
    const k = tri.n.indexOf(oldId);
    if (k >= 0) tri.n[k] = newId;
  }

  /** Split triangle t into 3 by inserting vertex pi in its interior. */
  private split3(t: number, pi: number): void {
    const tri = this.tris[t];
    const [a, b, c] = tri.v;
    const [n0, n1, n2] = tri.n;
    const tB = this.tris.length;
    const tC = tB + 1;
    tri.v = [a, b, pi];
    tri.n = [tB, tC, n2];
    this.tris.push({ v: [b, c, pi], n: [tC, t, n0], alive: true });
    this.tris.push({ v: [c, a, pi], n: [t, tB, n1], alive: true });
    this.replaceNeighbor(n0, t, tB);
    this.replaceNeighbor(n1, t, tC);
    this.legalizeAll([[t, 2], [tB, 2], [tC, 2]], pi);
  }

  /** Split triangles t1 and its neighbor across edge ei into 4 (pi on the edge). */
  private split4(t1: number, ei: number, pi: number): void {
    const tri1 = this.tris[t1];
    const t2 = tri1.n[ei];
    const tri2 = this.tris[t2];
    const a = tri1.v[(ei + 1) % 3];
    const b = tri1.v[(ei + 2) % 3];
    const c = tri1.v[ei];
    const ej = tri2.n.indexOf(t1);
    const d = tri2.v[ej];
    const nBC = tri1.n[(ei + 1) % 3];
    const nCA = tri1.n[(ei + 2) % 3];
    const nAD = tri2.n[(ej + 1) % 3];
    const nDB = tri2.n[(ej + 2) % 3];
    const tB = this.tris.length;
    const tD = tB + 1;
    tri1.v = [b, c, pi];
    tri1.n = [tB, tD, nBC];
    tri2.v = [a, d, pi];
    tri2.n = [tD, tB, nAD];
    this.tris.push({ v: [c, a, pi], n: [t2, t1, nCA], alive: true });
    this.tris.push({ v: [d, b, pi], n: [t1, t2, nDB], alive: true });
    this.replaceNeighbor(nCA, t1, tB);
    this.replaceNeighbor(nDB, t2, tD);
    this.legalizeAll([[t1, 2], [tB, 2], [t2, 2], [tD, 2]], pi);
  }

  /**
   * Lawson's flip algorithm: restore the empty-circumcircle property
   * around the freshly inserted vertex pi using an explicit edge stack.
   * Each stack entry is [triangleId, edgeIndexOppositePi].
   */
  private legalizeAll(seed: Array<[number, number]>, pi: number): void {
    const stack: Array<[number, number]> = [...seed];
    while (stack.length > 0) {
      const [t, ei] = stack.pop()!;
      const tri = this.tris[t];
      if (!tri.alive || tri.v[ei] !== pi) continue;
      const u = tri.n[ei];
      if (u < 0) continue;
      const ut = this.tris[u];
      if (!ut.alive) continue;
      const ej = ut.n.indexOf(t);
      if (ej < 0) continue;

      const A = tri.v[(ei + 1) % 3];
      const B = tri.v[(ei + 2) % 3];
      const P = pi;
      const Q = ut.v[ej];

      // Flip only when the quadrilateral (A, Q, B, P) is strictly convex.
      if (orient2d(this.pts[Q], this.pts[B], this.pts[P]) <= 0) continue;
      if (orient2d(this.pts[P], this.pts[A], this.pts[Q]) <= 0) continue;

      // Illegal edge: Q lies strictly inside the circumcircle of (A, B, P).
      // The threshold is relative to the determinant's own magnitude, so
      // only numerically unambiguous violations trigger a flip.
      const det = inCircle(this.pts[A], this.pts[B], this.pts[P], this.pts[Q]);
      const bound = inCircleBound(this.pts[A], this.pts[B], this.pts[P], this.pts[Q]);
      if (det <= 1e-12 * bound) continue;

      const nBP = tri.n[(ei + 1) % 3];
      const nPA = tri.n[(ei + 2) % 3];
      const nAQ = ut.n[(ej + 1) % 3];
      const nQB = ut.n[(ej + 2) % 3];

      // Replace diagonal (A,B) with (P,Q):
      //   t becomes (P, A, Q), u becomes (Q, B, P)  (both CCW).
      tri.v = [P, A, Q];
      tri.n = [nAQ, u, nPA];
      ut.v = [Q, B, P];
      ut.n = [nBP, t, nQB];
      this.replaceNeighbor(nAQ, u, t);
      this.replaceNeighbor(nBP, t, u);

      stack.push([t, 0], [u, 2]);
    }
  }

  private walkHull(boundaryEdges: Array<[number, number]>): number[] {
    if (boundaryEdges.length === 0) return [];
    const next = new Map<number, number>();
    for (const [from, to] of boundaryEdges) next.set(from, to);
    const start = boundaryEdges[0][0];
    const hull: number[] = [start];
    let cur = start;
    for (let guard = 0; guard <= boundaryEdges.length; guard++) {
      const nxt = next.get(cur);
      if (nxt === undefined || nxt === start) break;
      hull.push(nxt);
      cur = nxt;
    }
    return hull;
  }
}
