import type { ClipVertex } from "./types.ts";

type PlaneFn = (v: ClipVertex) => number;

/** Sutherland–Hodgman against a single plane; all fields interpolated linearly in clip space. */
function clipPlane(poly: ClipVertex[], dist: PlaneFn): ClipVertex[] {
  const out: ClipVertex[] = [];
  for (let i = 0; i < poly.length; i++) {
    const a = poly[i];
    const b = poly[(i + 1) % poly.length];
    const da = dist(a);
    const db = dist(b);
    if (da >= 0) out.push(a);
    if (da >= 0 !== db >= 0) {
      const t = da / (da - db);
      out.push({
        x: a.x + t * (b.x - a.x),
        y: a.y + t * (b.y - a.y),
        z: a.z + t * (b.z - a.z),
        w: a.w + t * (b.w - a.w),
        attrs: a.attrs.map((av, k) => av + t * (b.attrs[k] - av)),
      });
    }
  }
  return out;
}

/**
 * Clip a triangle against near (z >= -w) and far (z <= w) planes.
 * Together they also reject w <= 0 reflections behind the camera.
 * Returns 0..2 triangles (fan triangulation of the clipped polygon).
 */
export function clipTriangle(verts: [ClipVertex, ClipVertex, ClipVertex]): ClipVertex[][] {
  let poly = clipPlane(verts, (v) => v.z + v.w);
  poly = clipPlane(poly, (v) => v.w - v.z);
  const tris: ClipVertex[][] = [];
  for (let i = 1; i + 1 < poly.length; i++) {
    tris.push([poly[0], poly[i], poly[i + 1]]);
  }
  return tris;
}
