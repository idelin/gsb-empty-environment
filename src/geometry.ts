export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export function fullRect(w: number, h: number): Rect {
  return { x: 0, y: 0, w, h };
}

export function intersectRect(a: Rect, b: Rect): Rect | null {
  const x0 = Math.max(a.x, b.x);
  const y0 = Math.max(a.y, b.y);
  const x1 = Math.min(a.x + a.w, b.x + b.w);
  const y1 = Math.min(a.y + a.h, b.y + b.h);
  if (x1 <= x0 || y1 <= y0) return null;
  return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 };
}

export function unionRect(a: Rect, b: Rect): Rect {
  const x0 = Math.min(a.x, b.x);
  const y0 = Math.min(a.y, b.y);
  const x1 = Math.max(a.x + a.w, b.x + b.w);
  const y1 = Math.max(a.y + a.h, b.y + b.h);
  return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 };
}

export type Pt = [number, number];

export function polygonSignedArea(pts: readonly Pt[]): number {
  let s = 0;
  for (let i = 0; i < pts.length; i++) {
    const p = pts[i];
    const q = pts[(i + 1) % pts.length];
    s += p[0] * q[1] - q[0] * p[1];
  }
  return s / 2;
}

function crossEx(ax: number, ay: number, bx: number, by: number, px: number, py: number): number {
  return (bx - ax) * (py - ay) - (by - ay) * (px - ax);
}

export function clipConvex(subject: readonly Pt[], clip: readonly Pt[]): Pt[] {
  let out: Pt[] = subject.slice();
  for (let i = 0; i < clip.length && out.length > 0; i++) {
    const a = clip[i];
    const b = clip[(i + 1) % clip.length];
    const inp = out;
    out = [];
    for (let j = 0; j < inp.length; j++) {
      const cur = inp[j];
      const prv = inp[(j + inp.length - 1) % inp.length];
      const curIn = crossEx(a[0], a[1], b[0], b[1], cur[0], cur[1]) >= -1e-12;
      const prvIn = crossEx(a[0], a[1], b[0], b[1], prv[0], prv[1]) >= -1e-12;
      if (curIn) {
        if (!prvIn) out.push(edgeCross(prv, cur, a, b));
        out.push(cur);
      } else if (prvIn) {
        out.push(edgeCross(prv, cur, a, b));
      }
    }
  }
  return out;
}

function edgeCross(p1: Pt, p2: Pt, a: Pt, b: Pt): Pt {
  const d1 = crossEx(a[0], a[1], b[0], b[1], p1[0], p1[1]);
  const d2 = crossEx(a[0], a[1], b[0], b[1], p2[0], p2[1]);
  const t = d1 / (d1 - d2);
  return [p1[0] + t * (p2[0] - p1[0]), p1[1] + t * (p2[1] - p1[1])];
}

export function intersectionArea(subject: readonly Pt[], clip: readonly Pt[]): number {
  const clipped = clipConvex(subject, clip);
  return clipped.length >= 3 ? Math.abs(polygonSignedArea(clipped)) : 0;
}

export function ensureCcw(poly: readonly Pt[]): Pt[] {
  if (poly.length < 3) return poly.slice();
  return polygonSignedArea(poly) < 0 ? poly.slice().reverse() : poly.slice();
}

export function pointInConvex(poly: readonly Pt[], x: number, y: number, eps = 1e-9): boolean {
  for (let i = 0; i < poly.length; i++) {
    const a = poly[i];
    const b = poly[(i + 1) % poly.length];
    if (crossEx(a[0], a[1], b[0], b[1], x, y) < -eps) return false;
  }
  return true;
}

export function intersectConvex(a: readonly Pt[], b: readonly Pt[]): Pt[] {
  if (a.length < 3 || b.length < 3) return [];
  const clipped = clipConvex(ensureCcw(a), ensureCcw(b));
  return clipped.length >= 3 ? clipped : [];
}
