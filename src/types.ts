import { Rational } from './rational.ts';
import type { RationalInput } from './rational.ts';

export type Axis = 'x' | 'y';
export const AXES: readonly Axis[] = ['x', 'y'];

export interface Vec {
  readonly x: Rational;
  readonly y: Rational;
}

export function vec(x: RationalInput, y: RationalInput): Vec {
  return { x: Rational.of(x), y: Rational.of(y) };
}

export function vecZero(): Vec {
  return { x: Rational.zero(), y: Rational.zero() };
}

export function vecAdd(a: Vec, b: Vec): Vec {
  return { x: a.x.add(b.x), y: a.y.add(b.y) };
}

export function vecSub(a: Vec, b: Vec): Vec {
  return { x: a.x.sub(b.x), y: a.y.sub(b.y) };
}

export function vecScale(a: Vec, k: RationalInput): Vec {
  const r = Rational.of(k);
  return { x: a.x.mul(r), y: a.y.mul(r) };
}

export function vecIsZero(a: Vec): boolean {
  return a.x.isZero() && a.y.isZero();
}

export function vecEq(a: Vec, b: Vec): boolean {
  return a.x.eq(b.x) && a.y.eq(b.y);
}

export function vecGet(v: Vec, axis: Axis): Rational {
  return axis === 'x' ? v.x : v.y;
}

export function vecSet(v: Vec, axis: Axis, value: Rational): Vec {
  return axis === 'x' ? { x: value, y: v.y } : { x: v.x, y: value };
}

export function vecToString(v: Vec): string {
  return `(${v.x.toString()}, ${v.y.toString()})`;
}

/** chain: 触边界后剩余量沿祖先链传递；lock: 边界锁定，剩余量在本层舍弃。 */
export type OverscrollPolicy = 'chain' | 'lock';

export type ScrollSource =
  | { readonly kind: 'user' }
  | { readonly kind: 'programmatic'; readonly token: string }
  | { readonly kind: 'inertia'; readonly of: number };

export interface LayerRecord {
  readonly nodeId: string;
  /** 本层实际作用的增量（若发生轴变换，为变换后的值）。 */
  readonly received: Vec;
  /** 轴变换前的增量；仅本事务中执行变换的那一层非空。 */
  readonly preTransform: Vec | null;
  readonly consumed: Vec;
  readonly passed: Vec;
  readonly discarded: Vec;
  /** 捕获容器变化后，被重定向到达的层。 */
  readonly rerouted: boolean;
}

export interface ScrollTransaction {
  readonly id: number;
  readonly source: ScrollSource;
  readonly targetId: string;
  readonly initialDelta: Vec;
  readonly layers: LayerRecord[];
  /** 越过根节点仍未被消费的逃逸量。 */
  escaped: Vec;
  /** 重放用：事务前的全部滚动位置与捕获节点。 */
  readonly preState: ReadonlyMap<string, Vec>;
  readonly preCapture: string | null;
}
