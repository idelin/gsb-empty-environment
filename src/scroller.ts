import { ListModel, type ItemSpec, type MeasureToken } from "./list-model.ts";

/** 滚动锚点：稳定条目 ID + 条目内部偏移。 */
export interface Anchor {
  readonly id: string;
  readonly offset: number;
}

export interface VisibleRange {
  /** 第一个可见（含部分可见）条目的下标。 */
  readonly start: number;
  /** 最后一个可见条目下标 + 1。 */
  readonly end: number;
  readonly ids: string[];
}

/**
 * 可变行高列表的滚动控制器。
 *
 * 公开后备规则：
 * - 列表结构/高度变化时，保持锚点（条目 ID + 内部偏移）的视觉位置不变，
 *   即锚点之前的高度增减多少，scrollTop 就同步增减多少；
 * - 锚点条目被删除 → 锚点落到原位置的后继条目（offset 归零）；
 *   无后继 → 落到前驱；列表为空 → 锚点清空；
 * - 迟到/过期测量（令牌失效）被忽略，不影响滚动位置；
 * - 零高行（如折叠分组的子项）不参与按偏移查找，不会导致查找死循环；
 * - 尚未发生任何滚动时锚点为 null，列表保持顶部对齐。
 */
export class VirtualList {
  readonly model: ListModel;
  viewportHeight: number;
  scrollTop = 0;
  private anchor: Anchor | null = null;

  constructor(model: ListModel, viewportHeight: number) {
    this.model = model;
    this.viewportHeight = viewportHeight;
  }

  getAnchor(): Anchor | null {
    return this.anchor ? { ...this.anchor } : null;
  }

  maxScroll(): number {
    return Math.max(0, this.model.totalHeight() - this.viewportHeight);
  }

  /** 用户滚动（正向或反向）：夹取到合法范围并以当前位置重建锚点。 */
  onScroll(y: number): void {
    this.scrollTop = this.clamp(y);
    this.recomputeAnchor();
  }

  /** 跳转到指定条目，使其（加上内部偏移后）对齐视口顶部。 */
  jumpTo(id: string, offset = 0): boolean {
    if (!this.model.has(id)) return false;
    this.scrollTop = this.clamp(this.model.offsetOf(id) + Math.max(0, offset));
    this.recomputeAnchor();
    return true;
  }

  /** 跳转到绝对偏移。 */
  jumpToOffset(y: number): void {
    this.onScroll(y);
  }

  insert(index: number, spec: ItemSpec): void {
    this.preserveAnchor(() => this.model.insert(index, spec));
  }

  remove(id: string): boolean {
    return this.preserveAnchor(() => this.model.remove(id));
  }

  move(id: string, toIndex: number): void {
    this.preserveAnchor(() => this.model.move(id, toIndex));
  }

  setCollapsed(id: string, collapsed: boolean): void {
    this.preserveAnchor(() => this.model.setCollapsed(id, collapsed));
  }

  applyMeasure(token: MeasureToken, height: number): boolean {
    return this.preserveAnchor(() => this.model.applyMeasure(token, height));
  }

  /** 当前可见范围；零高行不会使查找或遍历陷入死循环。 */
  visibleRange(): VisibleRange {
    const size = this.model.size;
    const start = this.model.index.findIndex(this.scrollTop);
    if (start >= size) {
      return { start: size, end: size, ids: [] };
    }
    const bottom = this.scrollTop + this.viewportHeight;
    const ids: string[] = [];
    let i = start;
    while (i < size && this.model.index.prefix(i) < bottom) {
      ids.push(this.model.idAt(i));
      i++;
    }
    return { start, end: i, ids };
  }

  private preserveAnchor<T>(mutate: () => T): T {
    const anchorId =
      this.anchor !== null && this.model.has(this.anchor.id)
        ? this.anchor.id
        : null;
    const anchorIndex = anchorId !== null ? this.model.indexOf(anchorId) : -1;
    const oldPos = anchorId !== null ? this.model.offsetOf(anchorId) : null;

    const result = mutate();

    if (this.anchor !== null && !this.model.has(this.anchor.id)) {
      this.anchor = this.fallbackAnchor(anchorIndex);
    }
    if (oldPos !== null && this.anchor !== null) {
      const newPos = this.model.offsetOf(this.anchor.id);
      this.scrollTop += newPos - oldPos;
    }
    this.scrollTop = this.clamp(this.scrollTop);
    return result;
  }

  /** 锚点条目被删除后的后备选择：后继 → 前驱 → 空。 */
  private fallbackAnchor(deletedIndex: number): Anchor | null {
    const size = this.model.size;
    if (size === 0) return null;
    const i = Math.min(Math.max(deletedIndex, 0), size - 1);
    return { id: this.model.idAt(i), offset: 0 };
  }

  private recomputeAnchor(): void {
    const size = this.model.size;
    if (size === 0) {
      this.anchor = null;
      return;
    }
    const idx = this.model.index.findIndex(this.scrollTop);
    if (idx >= size) {
      this.anchor = null;
      return;
    }
    const id = this.model.idAt(idx);
    this.anchor = { id, offset: this.scrollTop - this.model.offsetOf(id) };
  }

  private clamp(y: number): number {
    if (!Number.isFinite(y)) return this.scrollTop;
    return Math.min(Math.max(y, 0), this.maxScroll());
  }
}
