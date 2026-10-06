import { Fenwick } from './fenwick.ts';

export type ItemId = string;

export interface LayoutItem {
  id: ItemId;
}

export type BatchOp =
  | { type: 'prepend'; items: LayoutItem[] }
  | { type: 'insert'; index: number; items: LayoutItem[] }
  | { type: 'remove'; ids: ItemId[] }
  | { type: 'measure'; id: ItemId; height: number }
  | { type: 'unmeasure'; id: ItemId };

export interface Anchor {
  id: ItemId;
  offsetIn: number;
}

export interface LocateResult {
  index: number;
  offsetIn: number;
}

export interface VisibleRange {
  startIndex: number;
  endIndex: number;
}

export interface ChatLayoutOptions {
  estimatedHeight?: number;
}

interface AnchorTarget {
  id: ItemId;
  offsetIn: number;
  toBottom: boolean;
}

export class ChatLayout {
  readonly estimatedHeight: number;
  private items: LayoutItem[] = [];
  private heights: number[] = [];
  private measured = new Map<ItemId, number>();
  private positions = new Map<ItemId, number>();
  private tree = new Fenwick();

  constructor(options: ChatLayoutOptions = {}) {
    const estimate = options.estimatedHeight ?? 50;
    ChatLayout.assertHeight(estimate);
    this.estimatedHeight = estimate;
  }

  get count(): number {
    return this.items.length;
  }

  get totalHeight(): number {
    return this.tree.total();
  }

  has(id: ItemId): boolean {
    return this.positions.has(id);
  }

  isMeasured(id: ItemId): boolean {
    return this.measured.has(id);
  }

  idAt(index: number): ItemId {
    const item = this.items[index];
    if (!item) throw new RangeError(`index ${index} out of range`);
    return item.id;
  }

  indexOf(id: ItemId): number {
    const index = this.positions.get(id);
    if (index === undefined) throw new RangeError(`unknown item id: ${id}`);
    return index;
  }

  heightOf(id: ItemId): number {
    return this.heights[this.indexOf(id)];
  }

  offsetOfIndex(index: number): number {
    if (index < 0 || index >= this.items.length) {
      throw new RangeError(`index ${index} out of range`);
    }
    return this.tree.prefixSum(index);
  }

  offsetOf(id: ItemId): number {
    return this.offsetOfIndex(this.indexOf(id));
  }

  locate(offset: number): LocateResult {
    const count = this.items.length;
    if (count === 0) return { index: -1, offsetIn: 0 };
    const clamped = Math.min(Math.max(offset, 0), this.tree.total());
    const index = this.tree.lowerBound(clamped);
    if (index >= count) {
      return { index: count - 1, offsetIn: this.heights[count - 1] };
    }
    return { index, offsetIn: clamped - this.tree.prefixSum(index) };
  }

  visibleRange(scrollOffset: number, viewportHeight: number): VisibleRange {
    if (this.items.length === 0 || viewportHeight <= 0) {
      return { startIndex: -1, endIndex: -1 };
    }
    const start = this.locate(scrollOffset).index;
    const end = this.locate(scrollOffset + viewportHeight).index;
    return { startIndex: start, endIndex: end };
  }

  captureAnchor(scrollOffset: number): Anchor | null {
    const { index, offsetIn } = this.locate(scrollOffset);
    if (index < 0) return null;
    return { id: this.items[index].id, offsetIn };
  }

  measure(id: ItemId, height: number): boolean {
    if (!this.positions.has(id)) return false;
    this.setMeasurement(id, height);
    return true;
  }

  unmeasure(id: ItemId): boolean {
    if (!this.positions.has(id)) return false;
    this.clearMeasurement(id);
    return true;
  }

  applyBatch(ops: BatchOp[], anchor?: Anchor | null): number | undefined {
    const target = anchor ? this.resolveAnchor(anchor, ops) : null;
    for (const op of ops) this.applyOp(op);
    if (!anchor || !target) return undefined;
    const index = this.positions.get(target.id);
    if (index === undefined) return undefined;
    const height = this.heights[index];
    const within = target.toBottom
      ? height
      : Math.min(Math.max(target.offsetIn, 0), height);
    return this.tree.prefixSum(index) + within;
  }

  update(ops: BatchOp[], scrollOffset: number): number {
    const anchor = this.captureAnchor(scrollOffset);
    const adjusted = this.applyBatch(ops, anchor);
    if (adjusted !== undefined) return adjusted;
    return Math.min(Math.max(scrollOffset, 0), this.totalHeight);
  }

  private resolveAnchor(anchor: Anchor, ops: BatchOp[]): AnchorTarget | null {
    const removed = new Set<ItemId>();
    for (const op of ops) {
      if (op.type === 'remove') {
        for (const id of op.ids) removed.add(id);
      }
    }
    const index = this.positions.get(anchor.id);
    if (index === undefined) return null;
    if (!removed.has(anchor.id)) {
      return { id: anchor.id, offsetIn: anchor.offsetIn, toBottom: false };
    }
    for (let i = index + 1; i < this.items.length; i++) {
      const candidate = this.items[i];
      if (!removed.has(candidate.id)) {
        return { id: candidate.id, offsetIn: 0, toBottom: false };
      }
    }
    for (let i = index - 1; i >= 0; i--) {
      const candidate = this.items[i];
      if (!removed.has(candidate.id)) {
        return { id: candidate.id, offsetIn: 0, toBottom: true };
      }
    }
    return null;
  }

  private applyOp(op: BatchOp): void {
    switch (op.type) {
      case 'prepend':
        this.insertItems(0, op.items);
        break;
      case 'insert':
        this.insertItems(op.index, op.items);
        break;
      case 'remove':
        this.removeItems(op.ids);
        break;
      case 'measure':
        if (this.positions.has(op.id)) this.setMeasurement(op.id, op.height);
        break;
      case 'unmeasure':
        if (this.positions.has(op.id)) this.clearMeasurement(op.id);
        break;
    }
  }

  private insertItems(index: number, items: LayoutItem[]): void {
    if (items.length === 0) return;
    const at = Math.max(0, Math.min(index, this.items.length));
    for (const item of items) {
      if (this.positions.has(item.id)) {
        throw new Error(`duplicate item id: ${item.id}`);
      }
    }
    const seen = new Set<ItemId>();
    for (const item of items) {
      if (seen.has(item.id)) throw new Error(`duplicate item id: ${item.id}`);
      seen.add(item.id);
    }
    this.items.splice(at, 0, ...items);
    const inserted = items.map(() => this.estimatedHeight);
    this.heights.splice(at, 0, ...inserted);
    this.rebuild();
  }

  private removeItems(ids: ItemId[]): void {
    if (ids.length === 0) return;
    const removed = new Set(ids);
    const keptItems: LayoutItem[] = [];
    const keptHeights: number[] = [];
    for (let i = 0; i < this.items.length; i++) {
      if (removed.has(this.items[i].id)) continue;
      keptItems.push(this.items[i]);
      keptHeights.push(this.heights[i]);
    }
    if (keptItems.length === this.items.length) return;
    this.items = keptItems;
    this.heights = keptHeights;
    for (const id of removed) this.measured.delete(id);
    this.rebuild();
  }

  private setMeasurement(id: ItemId, height: number): void {
    ChatLayout.assertHeight(height);
    const index = this.positions.get(id);
    if (index === undefined) return;
    const previous = this.heights[index];
    this.measured.set(id, height);
    this.heights[index] = height;
    this.tree.add(index, height - previous);
  }

  private clearMeasurement(id: ItemId): void {
    const index = this.positions.get(id);
    if (index === undefined) return;
    const previous = this.heights[index];
    this.measured.delete(id);
    this.heights[index] = this.estimatedHeight;
    this.tree.add(index, this.estimatedHeight - previous);
  }

  private rebuild(): void {
    this.positions.clear();
    for (let i = 0; i < this.items.length; i++) {
      this.positions.set(this.items[i].id, i);
    }
    this.tree.reset(this.heights);
  }

  private static assertHeight(height: number): void {
    if (!Number.isFinite(height) || height < 0) {
      throw new RangeError(`height must be a finite number >= 0, got ${height}`);
    }
  }
}
