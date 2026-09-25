export type Axis = 'x' | 'y';

export interface Vec2 {
  x: number;
  y: number;
}

export type Axes = 'both' | 'x' | 'y' | 'none';

export type AxisTransform = 'none' | 'swap';

export const EDGES = ['top', 'bottom', 'left', 'right'] as const;
export type Edge = (typeof EDGES)[number];

export function edgeOf(axis: Axis, dir: 1 | -1): Edge {
  return axis === 'y' ? (dir > 0 ? 'bottom' : 'top') : dir > 0 ? 'right' : 'left';
}

export function vec(x = 0, y = 0): Vec2 {
  return { x, y };
}

export function cloneVec(v: Vec2): Vec2 {
  return { x: v.x, y: v.y };
}

export function addVec(a: Vec2, b: Vec2): Vec2 {
  return { x: a.x + b.x, y: a.y + b.y };
}

export function subVec(a: Vec2, b: Vec2): Vec2 {
  return { x: a.x - b.x, y: a.y - b.y };
}

export function isZeroVec(v: Vec2): boolean {
  return v.x === 0 && v.y === 0;
}
