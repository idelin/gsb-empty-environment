import { Rational } from './rational.ts';
import type { Axis, OverscrollPolicy, ScrollSource, Vec } from './types.ts';
import { vecGet, vecSet, vecZero } from './types.ts';

export interface Rect {
  readonly x: Rational;
  readonly y: Rational;
  readonly width: Rational;
  readonly height: Rational;
}

export interface ScrollNodeOptions {
  readonly viewport: Vec;
  readonly content: Vec;
  readonly axes: readonly Axis[];
  readonly overscroll?: { readonly x?: OverscrollPolicy; readonly y?: OverscrollPolicy };
  /** 轴变换（如横向条带把纵向滚轮换算成横向）。每事务至多应用一次，由引擎保证。 */
  readonly axisTransform?: (delta: Vec) => Vec;
  /** 根坐标系下的命中矩形，夹具给出，不依赖浏览器布局。 */
  readonly rect?: Rect;
}

export interface ScrollEvent {
  readonly node: ScrollNode;
  readonly position: Vec;
  readonly applied: Vec;
  readonly source: ScrollSource;
}

export class ScrollNode {
  readonly id: string;
  readonly viewport: Vec;
  readonly content: Vec;
  readonly axes: readonly Axis[];
  readonly overscroll: { readonly x: OverscrollPolicy; readonly y: OverscrollPolicy };
  readonly axisTransform?: (delta: Vec) => Vec;
  readonly rect: Rect;
  parent: ScrollNode | null = null;
  readonly children: ScrollNode[] = [];
  /** 当前滚动位置，精确有理数，永不舍入。 */
  scroll: Vec = vecZero();
  private listeners: Array<(event: ScrollEvent) => void> = [];

  constructor(id: string, options: ScrollNodeOptions) {
    this.id = id;
    this.viewport = options.viewport;
    this.content = options.content;
    this.axes = options.axes;
    this.overscroll = {
      x: options.overscroll?.x ?? 'chain',
      y: options.overscroll?.y ?? 'chain',
    };
    this.axisTransform = options.axisTransform;
    this.rect = options.rect ?? {
      x: Rational.zero(),
      y: Rational.zero(),
      width: options.viewport.x,
      height: options.viewport.y,
    };
  }

  addChild(child: ScrollNode): void {
    child.parent = this;
    this.children.push(child);
  }

  maxScroll(axis: Axis): Rational {
    const room = vecGet(this.content, axis).sub(vecGet(this.viewport, axis));
    return room.sign() < 0 ? Rational.zero() : room;
  }

  contains(point: Vec): boolean {
    return (
      point.x.cmp(this.rect.x) >= 0 &&
      point.x.cmp(this.rect.x.add(this.rect.width)) < 0 &&
      point.y.cmp(this.rect.y) >= 0 &&
      point.y.cmp(this.rect.y.add(this.rect.height)) < 0
    );
  }

  /** 应用增量，返回实际消费量（逐轴钳制到 [0, maxScroll]）。 */
  applyScroll(delta: Vec, source: ScrollSource): Vec {
    let consumed = vecZero();
    for (const axis of ['x', 'y'] as const) {
      consumed = vecSet(consumed, axis, this.consumeAxis(axis, vecGet(delta, axis)));
    }
    if (!consumed.x.isZero() || !consumed.y.isZero()) {
      this.emit({ node: this, position: this.scroll, applied: consumed, source });
    }
    return consumed;
  }

  private consumeAxis(axis: Axis, amount: Rational): Rational {
    if (!this.axes.includes(axis) || amount.isZero()) return Rational.zero();
    const max = this.maxScroll(axis);
    const current = vecGet(this.scroll, axis);
    let next = current.add(amount);
    if (next.sign() < 0) next = Rational.zero();
    if (next.cmp(max) > 0) next = max;
    const used = next.sub(current);
    this.scroll = vecSet(this.scroll, axis, next);
    return used;
  }

  /** 程序滚动：钳制后落位并派发事件，事件携带来源（含令牌）。 */
  scrollTo(position: Vec, source: ScrollSource): Vec {
    const clampedX = Rational.min(Rational.max(position.x, Rational.zero()), this.maxScroll('x'));
    const clampedY = Rational.min(Rational.max(position.y, Rational.zero()), this.maxScroll('y'));
    const applied = { x: clampedX.sub(this.scroll.x), y: clampedY.sub(this.scroll.y) };
    this.scroll = { x: clampedX, y: clampedY };
    if (!applied.x.isZero() || !applied.y.isZero()) {
      this.emit({ node: this, position: this.scroll, applied, source });
    }
    return applied;
  }

  onScroll(listener: (event: ScrollEvent) => void): () => void {
    this.listeners.push(listener);
    return () => {
      const i = this.listeners.indexOf(listener);
      if (i >= 0) this.listeners.splice(i, 1);
    };
  }

  private emit(event: ScrollEvent): void {
    for (const listener of [...this.listeners]) listener(event);
  }
}
