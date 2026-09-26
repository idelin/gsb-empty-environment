import type { ClipVertex, Face, Viewport } from "../src/types.ts";

export const VP64: Viewport = { x: 0, y: 0, width: 64, height: 64 };

/** Build a face from pixel-space coordinates (w = 1, constant ndc z). */
export function screenFace(
  id: number,
  pts: [number, number][],
  rgba: number[],
  vp: Viewport = VP64,
  ndcZ = 0,
): Face {
  const verts: ClipVertex[] = pts.map(([px, py]) => {
    const ndx = ((px - vp.x) / vp.width) * 2 - 1;
    const ndy = 1 - ((py - vp.y) / vp.height) * 2;
    return { x: ndx, y: ndy, z: ndcZ, w: 1, attrs: [...rgba] };
  });
  return { id, verts: [verts[0], verts[1], verts[2]] };
}

/** Build a face directly in clip space. */
export function clipFace(
  id: number,
  verts: { x: number; y: number; z: number; w: number; rgba: number[] }[],
): Face {
  const cv: ClipVertex[] = verts.map((v) => ({ x: v.x, y: v.y, z: v.z, w: v.w, attrs: [...v.rgba] }));
  return { id, verts: [cv[0], cv[1], cv[2]] };
}

/** Opaque quad (two triangles) covering the pixel rect [x0,x1) x [y0,y1). */
export function quadFaces(
  id0: number,
  x0: number,
  y0: number,
  x1: number,
  y1: number,
  rgba: number[],
  vp: Viewport = VP64,
  ndcZ = 0,
): Face[] {
  return [
    screenFace(id0, [[x0, y0], [x1, y0], [x1, y1]], rgba, vp, ndcZ),
    screenFace(id0 + 1, [[x0, y0], [x1, y1], [x0, y1]], rgba, vp, ndcZ),
  ];
}

export function xorshift(seed: number): () => number {
  let s = seed >>> 0 || 1;
  return () => {
    s ^= s << 13;
    s >>>= 0;
    s ^= s >> 17;
    s ^= s << 5;
    s >>>= 0;
    return s;
  };
}

export function shuffled(n: number, rand: () => number): number[] {
  const a = Array.from({ length: n }, (_, i) => i);
  for (let i = n - 1; i > 0; i--) {
    const j = rand() % (i + 1);
    const tmp = a[i];
    a[i] = a[j];
    a[j] = tmp;
  }
  return a;
}
