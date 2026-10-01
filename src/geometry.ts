/** 基础几何类型与运算。核心计算均在此完成，不依赖任何浏览器布局引擎。 */

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export type Direction = 'up' | 'down' | 'left' | 'right';

export const DIRECTIONS: readonly Direction[] = ['up', 'down', 'left', 'right'];

export function centerX(r: Rect): number {
  return r.x + r.w / 2;
}

export function centerY(r: Rect): number {
  return r.y + r.h / 2;
}

export function containsPoint(r: Rect, px: number, py: number): boolean {
  return px >= r.x && px <= r.x + r.w && py >= r.y && py <= r.y + r.h;
}

export function intersects(a: Rect, b: Rect): boolean {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}

export function containsRect(outer: Rect, inner: Rect): boolean {
  return (
    inner.x >= outer.x &&
    inner.y >= outer.y &&
    inner.x + inner.w <= outer.x + outer.w &&
    inner.y + inner.h <= outer.y + outer.h
  );
}

/** 方向的主轴分量：right/left 主轴为 x，up/down 主轴为 y。 */
export function primaryDelta(from: Rect, to: Rect, dir: Direction): number {
  switch (dir) {
    case 'right':
      return centerX(to) - centerX(from);
    case 'left':
      return centerX(from) - centerX(to);
    case 'down':
      return centerY(to) - centerY(from);
    case 'up':
      return centerY(from) - centerY(to);
  }
}

/** 方向的副轴（正交轴）偏移绝对值。 */
export function orthogonalDelta(from: Rect, to: Rect, dir: Direction): number {
  switch (dir) {
    case 'right':
    case 'left':
      return Math.abs(centerY(to) - centerY(from));
    case 'up':
    case 'down':
      return Math.abs(centerX(to) - centerX(from));
  }
}
