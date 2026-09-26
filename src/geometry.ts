export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Transform {
  tx: number;
  ty: number;
  sx: number;
  sy: number;
}

export const IDENTITY: Transform = { tx: 0, ty: 0, sx: 1, sy: 1 };

export function rect(x: number, y: number, w: number, h: number): Rect {
  return { x, y, w, h };
}

export function isEmpty(r: Rect): boolean {
  return r.w <= 0 || r.h <= 0;
}

export function intersect(a: Rect, b: Rect): Rect {
  const x = Math.max(a.x, b.x);
  const y = Math.max(a.y, b.y);
  const right = Math.min(a.x + a.w, b.x + b.w);
  const bottom = Math.min(a.y + a.h, b.y + b.h);
  return rect(x, y, Math.max(0, right - x), Math.max(0, bottom - y));
}

export function union(a: Rect, b: Rect): Rect {
  if (isEmpty(a)) return { ...b };
  if (isEmpty(b)) return { ...a };
  const x = Math.min(a.x, b.x);
  const y = Math.min(a.y, b.y);
  const right = Math.max(a.x + a.w, b.x + b.w);
  const bottom = Math.max(a.y + a.h, b.y + b.h);
  return rect(x, y, right - x, bottom - y);
}

export function inflate(r: Rect, d: number): Rect {
  return rect(r.x - d, r.y - d, r.w + 2 * d, r.h + 2 * d);
}

export function translate(r: Rect, dx: number, dy: number): Rect {
  return rect(r.x + dx, r.y + dy, r.w, r.h);
}

export function rectsEqual(a: Rect, b: Rect): boolean {
  return a.x === b.x && a.y === b.y && a.w === b.w && a.h === b.h;
}

export function composeTransform(outer: Transform, inner: Transform): Transform {
  return {
    tx: outer.tx + outer.sx * inner.tx,
    ty: outer.ty + outer.sy * inner.ty,
    sx: outer.sx * inner.sx,
    sy: outer.sy * inner.sy,
  };
}

// Axis-aligned, integer-conservative (floor min / ceil max) so the result
// always covers every device pixel the source rect touches.
export function transformRect(r: Rect, t: Transform): Rect {
  const x1 = t.tx + r.x * t.sx;
  const y1 = t.ty + r.y * t.sy;
  const x2 = t.tx + (r.x + r.w) * t.sx;
  const y2 = t.ty + (r.y + r.h) * t.sy;
  const x = Math.floor(Math.min(x1, x2));
  const y = Math.floor(Math.min(y1, y2));
  return rect(x, y, Math.ceil(Math.max(x1, x2)) - x, Math.ceil(Math.max(y1, y2)) - y);
}
