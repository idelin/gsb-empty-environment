/**
 * 2D 仿射变换（2x3 矩阵）：
 *   x' = a*x + c*y + e
 *   y' = b*x + d*y + f
 *
 * 本库支持的所有操作（平移/裁剪、缩放、翻转、90 度旋转）都是仿射变换，
 * 且保持轴对齐性：轴对齐半开矩形映射后仍是轴对齐半开矩形，可精确表示。
 */

import type { Point, Rect } from "./geometry.ts";
import { rect } from "./geometry.ts";

export interface Affine {
  a: number;
  b: number;
  c: number;
  d: number;
  e: number;
  f: number;
}

export const IDENTITY: Affine = { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 };

export function applyAffine(m: Affine, p: Point): Point {
  return {
    x: m.a * p.x + m.c * p.y + m.e,
    y: m.b * p.x + m.d * p.y + m.f,
  };
}

/**
 * 复合：compose(m2, m1) 表示先应用 m1 再应用 m2。
 */
export function composeAffine(m2: Affine, m1: Affine): Affine {
  return {
    a: m2.a * m1.a + m2.c * m1.b,
    b: m2.b * m1.a + m2.d * m1.b,
    c: m2.a * m1.c + m2.c * m1.d,
    d: m2.b * m1.c + m2.d * m1.d,
    e: m2.a * m1.e + m2.c * m1.f + m2.e,
    f: m2.b * m1.e + m2.d * m1.f + m2.f,
  };
}

export function invertAffine(m: Affine): Affine {
  const det = m.a * m.d - m.b * m.c;
  if (det === 0) {
    throw new RangeError("仿射矩阵不可逆（行列式为 0）");
  }
  return {
    a: m.d / det,
    b: -m.b / det,
    c: -m.c / det,
    d: m.a / det,
    e: (m.c * m.f - m.d * m.e) / det,
    f: (m.b * m.e - m.a * m.f) / det,
  };
}

/**
 * 轴对齐矩形经轴保持仿射映射：变换两个角点后重新归一化。
 * 半开语义保持不变：[x0,x1) 的像仍是半开区间。
 */
export function mapRectAffine(m: Affine, r: Rect): Rect {
  const p00 = applyAffine(m, { x: r.x0, y: r.y0 });
  const p11 = applyAffine(m, { x: r.x1, y: r.y1 });
  return rect(
    Math.min(p00.x, p11.x),
    Math.min(p00.y, p11.y),
    Math.max(p00.x, p11.x),
    Math.max(p00.y, p11.y),
  );
}

export function affineAlmostEqual(m: Affine, n: Affine, tol = 1e-9): boolean {
  return (
    Math.abs(m.a - n.a) <= tol &&
    Math.abs(m.b - n.b) <= tol &&
    Math.abs(m.c - n.c) <= tol &&
    Math.abs(m.d - n.d) <= tol &&
    Math.abs(m.e - n.e) <= tol &&
    Math.abs(m.f - n.f) <= tol
  );
}
