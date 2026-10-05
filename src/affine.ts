/**
 * 2D 仿射变换（仅平移/缩放/翻转/90°旋转的组合，矩阵始终保持轴对齐）。
 *
 *   x' = a*x + b*y + tx
 *   y' = c*x + d*y + ty
 *
 * 全程使用浮点精确运算，不做任何整数取整；
 * 取整只允许发生在最终栅格化（见 raster.ts）。
 */
export interface Affine {
  readonly a: number;
  readonly b: number;
  readonly c: number;
  readonly d: number;
  readonly tx: number;
  readonly ty: number;
}

export const IDENTITY: Affine = { a: 1, b: 0, c: 0, d: 1, tx: 0, ty: 0 };

export function translation(tx: number, ty: number): Affine {
  return { a: 1, b: 0, c: 0, d: 1, tx, ty };
}

export function scaling(sx: number, sy: number): Affine {
  return { a: sx, b: 0, c: 0, d: sy, tx: 0, ty: 0 };
}

/** 复合：compose(m, n) 表示「先应用 n，再应用 m」，即 m ∘ n。 */
export function compose(m: Affine, n: Affine): Affine {
  return {
    a: m.a * n.a + m.b * n.c,
    b: m.a * n.b + m.b * n.d,
    c: m.c * n.a + m.d * n.c,
    d: m.c * n.b + m.d * n.d,
    tx: m.a * n.tx + m.b * n.ty + m.tx,
    ty: m.c * n.tx + m.d * n.ty + m.ty,
  };
}

/** 求逆。本库中所有变换均可逆（缩放因子非零），行列式不会为 0。 */
export function invert(m: Affine): Affine {
  const det = m.a * m.d - m.b * m.c;
  if (det === 0) throw new Error("singular affine matrix");
  const a = m.d / det;
  const b = -m.b / det;
  const c = -m.c / det;
  const d = m.a / det;
  return {
    a, b, c, d,
    tx: -(a * m.tx + b * m.ty),
    ty: -(c * m.tx + d * m.ty),
  };
}
