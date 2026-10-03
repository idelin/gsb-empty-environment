import { orient2d, inCircle, dist, type Point } from './geometry.ts';
import { buildMesh, type TriangleMesh } from './mesh.ts';

/**
 * Internal triangle record. `v` holds CCW internal vertex ids; ids 0..2 are
 * the supertriangle vertices, real point with external index i has id i + 3.
 * `n[k]` is the triangle adjacent across the edge opposite `v[k]` (-1 = none).
 */
interface Tri {
  v: [number, number, number];
  n: [number, number, number];
  alive: boolean;
}

const SUPER_VERTICES = 3;

/**
 * Delaunay triangulator for 2D point sets.
 *
 * Incremental construction: points are inserted one at a time into an
 * enclosing supertriangle; each insertion splits the containing triangle
 * (3-way, or 4-way when the point lands on an edge) and local legality is
 * restored with Lawson's edge-flipping algorithm. Triangles touching the
 * supertriangle vertices are stripped when the mesh is extracted.
 */
export class DelaunayTriangulator {
  private pts: Point[] = [];
  private sup: Point[] = [];
  private tris: Tri[] = [];
  private scale = 1;
  private last = 0;

  constructor(points: Point[] = []) {
    for (const p of points) this.insert(p);
  }

  get size(): number {
    return this.pts.length;
  }

  /** Insert a point; returns its index. Duplicate points are ignored. */
  insert(p: Point): number {
    const tol = 1e-9 * this.scale;
    const dup = this.pts.findIndex((q) => dist(q, p) <= tol);
    if (dup >= 0) return dup;

    this.pts.push(p);
    if (this.sup.length === 0 || !this.insideSuper(p)) {
      this.rebuild();
    } else {
      this.insertPoint(this.pts.length - 1 + SUPER_VERTICES);
    }
    return this.pts.length - 1;
  }

  /** Extract the compact topology mesh of the real points. */
  toMesh(): TriangleMesh {
    const triangles: [number, number, number][] = [];
    for (const tri of this.tris) {
      if (!tri.alive) continue;
      if (tri.v[0] < SUPER_VERTICES) continue;
      if (tri.v[1] < SUPER_VERTICES) continue;
      if (tri.v[2] < SUPER_VERTICES) continue;
      triangles.push([
        tri.v[0] - SUPER_VERTICES,
        tri.v[1] - SUPER_VERTICES,
        tri.v[2] - SUPER_VERTICES,
      ]);
    }
    return buildMesh(this.pts, triangles);
  }

  // ---------------------------------------------------------------- internal

  private pt(id: number): Point {
    return id < SUPER_VERTICES ? this.sup[id] : this.pts[id - SUPER_VERTICES];
  }

  private insideSuper(p: Point): boolean {
    const [a, b, c] = this.sup;
    return (
      orient2d(a, b, p) > 0 && orient2d(b, c, p) > 0 && orient2d(c, a, p) > 0
    );
  }

  /** (Re)build the supertriangle and reinsert every point seen so far. */
  private rebuild(): void {
    let minX = Infinity;
    let minY = Infinity;
    let maxX = -Infinity;
    let maxY = -Infinity;
    for (const p of this.pts) {
      if (p.x < minX) minX = p.x;
      if (p.y < minY) minY = p.y;
      if (p.x > maxX) maxX = p.x;
      if (p.y > maxY) maxY = p.y;
    }
    const m = Math.max(maxX - minX, maxY - minY, 1);
    const cx = (minX + maxX) / 2;
    const cy = (minY + maxY) / 2;
    this.scale = m;

    const sup = [
      { x: cx - 1024 * m, y: cy - m },
      { x: cx + 1024 * m, y: cy - m },
      { x: cx, y: cy + 1024 * m },
    ];
    if (orient2d(sup[0], sup[1], sup[2]) < 0) {
      [sup[1], sup[2]] = [sup[2], sup[1]];
    }
    this.sup = sup;
    this.tris = [{ v: [0, 1, 2], n: [-1, -1, -1], alive: true }];
    this.last = 0;
    for (let i = 0; i < this.pts.length; i++) {
      this.insertPoint(i + SUPER_VERTICES);
    }
  }

  /** Visibility walk from the most recent triangle, with a full-scan fallback. */
  private locate(p: Point): number {
    let t = this.last;
    if (t >= this.tris.length || !this.tris[t].alive) {
      t = this.tris.findIndex((tri) => tri.alive);
    }
    for (let guard = 0; guard <= this.tris.length; guard++) {
      const tri = this.tris[t];
      let moved = false;
      let escaped = false;
      for (let k = 0; k < 3; k++) {
        const o = orient2d(
          this.pt(tri.v[(k + 1) % 3]),
          this.pt(tri.v[(k + 2) % 3]),
          p,
        );
        if (o < 0) {
          const nt = tri.n[k];
          if (nt >= 0 && this.tris[nt].alive) {
            t = nt;
            moved = true;
          } else {
            escaped = true;
          }
          break;
        }
      }
      if (!moved) {
        if (!escaped) {
          this.last = t;
          return t;
        }
        break; // walk left the triangulation: fall through to the full scan
      }
    }
    const tol = 1e-9 * this.scale * this.scale;
    for (let i = 0; i < this.tris.length; i++) {
      const tri = this.tris[i];
      if (!tri.alive) continue;
      let inside = true;
      for (let k = 0; k < 3; k++) {
        const o = orient2d(
          this.pt(tri.v[(k + 1) % 3]),
          this.pt(tri.v[(k + 2) % 3]),
          p,
        );
        if (o < -tol) {
          inside = false;
          break;
        }
      }
      if (inside) {
        this.last = i;
        return i;
      }
    }
    throw new Error('point location failed: point outside triangulation');
  }

  private insertPoint(id: number): void {
    const p = this.pt(id);
    const t = this.locate(p);
    const tri = this.tris[t];
    const tol = 1e-9 * this.scale * this.scale;

    let onEdge = -1;
    for (let k = 0; k < 3; k++) {
      const o = orient2d(
        this.pt(tri.v[(k + 1) % 3]),
        this.pt(tri.v[(k + 2) % 3]),
        p,
      );
      if (Math.abs(o) <= tol) {
        if (onEdge >= 0) {
          // Degenerate (on a vertex); fall back to a plain 3-way split.
          onEdge = -1;
          break;
        }
        onEdge = k;
      }
    }

    if (onEdge >= 0 && tri.n[onEdge] >= 0 && this.tris[tri.n[onEdge]].alive) {
      this.split4(t, onEdge, id);
    } else {
      this.split3(t, id);
    }
  }

  /** Split triangle t into three by connecting its vertices to point id. */
  private split3(t: number, id: number): void {
    const { v, n } = this.tris[t];
    const [a, b, c] = v;

    const t1 = this.tris.length;
    const t2 = t1 + 1;

    this.tris[t] = { v: [a, b, id], n: [t1, t2, n[2]], alive: true };
    this.tris.push({ v: [b, c, id], n: [t2, t, n[0]], alive: true });
    this.tris.push({ v: [c, a, id], n: [t, t1, n[1]], alive: true });

    this.replaceNeighbor(n[0], t, t1);
    this.replaceNeighbor(n[1], t, t2);

    this.legalizeEdge(t, 2);
    this.legalizeEdge(t1, 2);
    this.legalizeEdge(t2, 2);
  }

  /**
   * Split triangle t and its neighbor across edge k (which contains point id)
   * into four triangles.
   */
  private split4(t: number, k: number, id: number): void {
    const tri = this.tris[t];
    const apex = tri.v[k];
    const e1 = tri.v[(k + 1) % 3];
    const e2 = tri.v[(k + 2) % 3];
    const nApexE2 = tri.n[(k + 1) % 3]; // across edge (e2, apex)
    const nE1Apex = tri.n[(k + 2) % 3]; // across edge (apex, e1)

    const u = tri.n[k];
    const utri = this.tris[u];
    const j = utri.n.findIndex((x) => x === t);
    const d = utri.v[j];
    const nE1D = utri.n[(j + 1) % 3]; // across edge (e1, d)
    const nDE2 = utri.n[(j + 2) % 3]; // across edge (d, e2)

    const t1 = this.tris.length;
    const u1 = t1 + 1;

    // T0 = (apex, e1, p) reuses slot t; U0 = (d, e2, p) reuses slot u.
    this.tris[t] = { v: [apex, e1, id], n: [u1, t1, nE1Apex], alive: true };
    this.tris.push({ v: [id, e2, apex], n: [nApexE2, t, u], alive: true });
    this.tris[u] = { v: [d, e2, id], n: [t1, u1, nDE2], alive: true };
    this.tris.push({ v: [id, e1, d], n: [nE1D, u, t], alive: true });

    this.replaceNeighbor(nApexE2, t, t1);
    this.replaceNeighbor(nE1D, u, u1);

    this.legalizeEdge(t, 2);
    this.legalizeEdge(t1, 0);
    this.legalizeEdge(u, 2);
    this.legalizeEdge(u1, 0);
  }

  /**
   * Lawson's legalization: `tris[t].v[i]` is the freshly inserted point.
   * If the edge opposite it is illegal, flip it and recurse on the two
   * new edges opposite the inserted point.
   */
  private legalizeEdge(t: number, i: number): void {
    const tri = this.tris[t];
    const u = tri.n[i];
    if (u < 0) return;
    const utri = this.tris[u];
    if (!utri.alive) return;

    const j = utri.n.findIndex((x) => x === t);
    if (j < 0) return;

    const p = tri.v[i];
    const a = tri.v[(i + 1) % 3];
    const b = tri.v[(i + 2) % 3];
    const q = utri.v[j];

    // (a, b, p) is a cyclic permutation of CCW (p, a, b), hence CCW.
    if (inCircle(this.pt(a), this.pt(b), this.pt(p), this.pt(q)) <= 0) return;

    this.flip(t, i, u, j);
    this.legalizeEdge(t, 0); // p sits at index 0 of t after the flip
    this.legalizeEdge(u, 2); // p sits at index 2 of u after the flip
  }

  /** Replace the shared edge (a,b) of triangles t and u with (p,q). */
  private flip(t: number, i: number, u: number, j: number): void {
    const tri = this.tris[t];
    const utri = this.tris[u];

    const p = tri.v[i];
    const a = tri.v[(i + 1) % 3];
    const b = tri.v[(i + 2) % 3];
    const q = utri.v[j];

    const nBP = tri.n[(i + 1) % 3]; // across edge (b, p)
    const nPA = tri.n[(i + 2) % 3]; // across edge (p, a)
    const nAQ = utri.n[(j + 1) % 3]; // across edge (a, q)
    const nQB = utri.n[(j + 2) % 3]; // across edge (q, b)

    this.tris[t] = { v: [p, a, q], n: [nAQ, u, nPA], alive: true };
    this.tris[u] = { v: [q, b, p], n: [nBP, t, nQB], alive: true };

    this.replaceNeighbor(nAQ, u, t);
    this.replaceNeighbor(nBP, t, u);
  }

  private replaceNeighbor(x: number, oldT: number, newT: number): void {
    if (x < 0) return;
    const n = this.tris[x].n;
    for (let k = 0; k < 3; k++) {
      if (n[k] === oldT) {
        n[k] = newT;
        return;
      }
    }
  }
}
