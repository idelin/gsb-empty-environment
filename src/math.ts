/** 2D affine matrix, same convention as Canvas: [a c e; b d f; 0 0 1]. */
export interface Matrix {
  a: number; b: number; c: number; d: number; e: number; f: number;
}

export const identity = (): Matrix => ({ a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 });

export const translation = (tx: number, ty: number): Matrix =>
  ({ a: 1, b: 0, c: 0, d: 1, e: tx, f: ty });

export const scaling = (sx: number, sy: number): Matrix =>
  ({ a: sx, b: 0, c: 0, d: sy, e: 0, f: 0 });

export const rotation = (rad: number): Matrix => {
  const cos = Math.cos(rad);
  const sin = Math.sin(rad);
  return { a: cos, b: sin, c: -sin, d: cos, e: 0, f: 0 };
};

/** this * other (apply other first, then this). */
export function multiply(m: Matrix, o: Matrix): Matrix {
  return {
    a: m.a * o.a + m.c * o.b,
    b: m.b * o.a + m.d * o.b,
    c: m.a * o.c + m.c * o.d,
    d: m.b * o.c + m.d * o.d,
    e: m.a * o.e + m.c * o.f + m.e,
    f: m.b * o.e + m.d * o.f + m.f,
  };
}

export function applyToPoint(m: Matrix, x: number, y: number): { x: number; y: number } {
  return { x: m.a * x + m.c * y + m.e, y: m.b * x + m.d * y + m.f };
}

export function invert(m: Matrix): Matrix {
  const det = m.a * m.d - m.b * m.c;
  if (det === 0) throw new Error('matrix not invertible');
  const inv = 1 / det;
  return {
    a: m.d * inv,
    b: -m.b * inv,
    c: -m.c * inv,
    d: m.a * inv,
    e: (m.c * m.f - m.d * m.e) * inv,
    f: (m.b * m.e - m.a * m.f) * inv,
  };
}

/** Largest absolute axis scale of the matrix; used for conservative expansions. */
export function maxScale(m: Matrix): number {
  const sx = Math.hypot(m.a, m.b);
  const sy = Math.hypot(m.c, m.d);
  return Math.max(sx, sy);
}

export interface Rect {
  x: number; y: number; width: number; height: number;
}

export const rect = (x: number, y: number, width: number, height: number): Rect =>
  ({ x, y, width, height });

export function isEmpty(r: Rect): boolean {
  return r.width <= 0 || r.height <= 0;
}

export function intersect(a: Rect, b: Rect): Rect | null {
  const x = Math.max(a.x, b.x);
  const y = Math.max(a.y, b.y);
  const right = Math.min(a.x + a.width, b.x + b.width);
  const bottom = Math.min(a.y + a.height, b.y + b.height);
  if (right <= x || bottom <= y) return null;
  return { x, y, width: right - x, height: bottom - y };
}

export function union(a: Rect, b: Rect): Rect {
  const x = Math.min(a.x, b.x);
  const y = Math.min(a.y, b.y);
  const right = Math.max(a.x + a.width, b.x + b.width);
  const bottom = Math.max(a.y + a.height, b.y + b.height);
  return { x, y, width: right - x, height: bottom - y };
}

export function unionAll(rects: Rect[]): Rect | null {
  let out: Rect | null = null;
  for (const r of rects) out = out === null ? r : union(out, r);
  return out;
}

export function expand(r: Rect, dx: number, dy: number): Rect {
  return { x: r.x - dx, y: r.y - dy, width: r.width + 2 * dx, height: r.height + 2 * dy };
}

export function offset(r: Rect, dx: number, dy: number): Rect {
  return { x: r.x + dx, y: r.y + dy, width: r.width, height: r.height };
}

/** Conservative (axis-aligned) device-space bounds of a rect under a matrix. */
export function transformRect(m: Matrix, r: Rect): Rect {
  const pts = [
    applyToPoint(m, r.x, r.y),
    applyToPoint(m, r.x + r.width, r.y),
    applyToPoint(m, r.x, r.y + r.height),
    applyToPoint(m, r.x + r.width, r.y + r.height),
  ];
  const xs = pts.map((p) => p.x);
  const ys = pts.map((p) => p.y);
  const minX = Math.min(...xs);
  const minY = Math.min(...ys);
  return { x: minX, y: minY, width: Math.max(...xs) - minX, height: Math.max(...ys) - minY };
}

/** Round outward to integer device pixels (conservative). */
export function roundOut(r: Rect): Rect {
  const x = Math.floor(r.x);
  const y = Math.floor(r.y);
  return {
    x,
    y,
    width: Math.ceil(r.x + r.width) - x,
    height: Math.ceil(r.y + r.height) - y,
  };
}

export function containsRect(outer: Rect, inner: Rect): boolean {
  return (
    inner.x >= outer.x &&
    inner.y >= outer.y &&
    inner.x + inner.width <= outer.x + outer.width &&
    inner.y + inner.height <= outer.y + outer.height
  );
}
