export interface Point {
  readonly x: number;
  readonly y: number;
}

export interface Size {
  readonly width: number;
  readonly height: number;
}

export interface Rect {
  readonly x: number;
  readonly y: number;
  readonly width: number;
  readonly height: number;
}

export interface Edges {
  readonly top: number;
  readonly right: number;
  readonly bottom: number;
  readonly left: number;
}

/**
 * A 2D affine transform using the CSS matrix convention:
 * x' = a*x + c*y + e, y' = b*x + d*y + f.
 */
export interface Affine {
  readonly a: number;
  readonly b: number;
  readonly c: number;
  readonly d: number;
  readonly e: number;
  readonly f: number;
}

export const IDENTITY: Affine = { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 };

export const ZERO_EDGES: Edges = { top: 0, right: 0, bottom: 0, left: 0 };

export function translation(tx: number, ty: number): Affine {
  return { a: 1, b: 0, c: 0, d: 1, e: tx, f: ty };
}

export function scaling(sx: number, sy: number): Affine {
  return { a: sx, b: 0, c: 0, d: sy, e: 0, f: 0 };
}

export function rotation(radians: number): Affine {
  const cos = Math.cos(radians);
  const sin = Math.sin(radians);
  return { a: cos, b: sin, c: -sin, d: cos, e: 0, f: 0 };
}

export function rotationDegrees(degrees: number): Affine {
  return rotation((degrees * Math.PI) / 180);
}

/** Returns outer ∘ inner. */
export function multiply(outer: Affine, inner: Affine): Affine {
  return {
    a: outer.a * inner.a + outer.c * inner.b,
    b: outer.b * inner.a + outer.d * inner.b,
    c: outer.a * inner.c + outer.c * inner.d,
    d: outer.b * inner.c + outer.d * inner.d,
    e: outer.a * inner.e + outer.c * inner.f + outer.e,
    f: outer.b * inner.e + outer.d * inner.f + outer.f,
  };
}

export function compose(...transforms: readonly Affine[]): Affine {
  return transforms.reduce<Affine>((result, next) => multiply(result, next), IDENTITY);
}

export function applyAffine(matrix: Affine, point: Point): Point {
  return {
    x: matrix.a * point.x + matrix.c * point.y + matrix.e,
    y: matrix.b * point.x + matrix.d * point.y + matrix.f,
  };
}

export function invertAffine(matrix: Affine): Affine {
  const determinant = matrix.a * matrix.d - matrix.b * matrix.c;
  if (Math.abs(determinant) < 1e-12) {
    throw new Error("Cannot invert a singular affine transform");
  }
  return {
    a: matrix.d / determinant,
    b: -matrix.b / determinant,
    c: -matrix.c / determinant,
    d: matrix.a / determinant,
    e: (matrix.c * matrix.f - matrix.d * matrix.e) / determinant,
    f: (matrix.b * matrix.e - matrix.a * matrix.f) / determinant,
  };
}

export function rectCorners(rect: Rect): readonly Point[] {
  return [
    { x: rect.x, y: rect.y },
    { x: rect.x + rect.width, y: rect.y },
    { x: rect.x + rect.width, y: rect.y + rect.height },
    { x: rect.x, y: rect.y + rect.height },
  ];
}

export function transformCorners(matrix: Affine, rect: Rect): readonly Point[] {
  return rectCorners(rect).map((point) => applyAffine(matrix, point));
}

export function pointInRect(point: Point, rect: Rect, epsilon = 1e-9): boolean {
  return (
    point.x >= rect.x - epsilon &&
    point.x <= rect.x + rect.width + epsilon &&
    point.y >= rect.y - epsilon &&
    point.y <= rect.y + rect.height + epsilon
  );
}

export function pointInViewport(point: Point, viewport: Size, epsilon = 1e-9): boolean {
  return pointInRect(
    point,
    { x: 0, y: 0, width: viewport.width, height: viewport.height },
    epsilon,
  );
}

/**
 * Point-in-convex-polygon test that is inclusive on edges. The polygons used
 * here are quadrilateral frame content boxes produced by an affine transform.
 */
export function pointInConvexPolygon(point: Point, polygon: readonly Point[], epsilon = 1e-7): boolean {
  if (polygon.length < 3) {
    return false;
  }

  let positive = false;
  let negative = false;

  for (let index = 0; index < polygon.length; index += 1) {
    const current = polygon[index];
    const next = polygon[(index + 1) % polygon.length];
    const cross =
      (next.x - current.x) * (point.y - current.y) -
      (next.y - current.y) * (point.x - current.x);
    if (cross > epsilon) {
      positive = true;
    } else if (cross < -epsilon) {
      negative = true;
    }
    if (positive && negative) {
      return false;
    }
  }

  return true;
}

export function intersectRects(first: Rect, second: Rect): Rect | null {
  const left = Math.max(first.x, second.x);
  const top = Math.max(first.y, second.y);
  const right = Math.min(first.x + first.width, second.x + second.width);
  const bottom = Math.min(first.y + first.height, second.y + second.height);
  if (right < left || bottom < top) {
    return null;
  }
  return { x: left, y: top, width: right - left, height: bottom - top };
}

export function rectsEqual(first: Rect, second: Rect, epsilon = 1e-9): boolean {
  return (
    Math.abs(first.x - second.x) <= epsilon &&
    Math.abs(first.y - second.y) <= epsilon &&
    Math.abs(first.width - second.width) <= epsilon &&
    Math.abs(first.height - second.height) <= epsilon
  );
}
