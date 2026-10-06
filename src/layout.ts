import { FenwickTree } from "./fenwick.ts";

export interface ChatItem {
  key: string;
  /** Measured height; omitted until the item has been rendered and measured. */
  height?: number;
}

export interface ItemLocation {
  index: number;
  key: string;
  /** Top edge offset of the item within the list. */
  top: number;
  height: number;
  /** Offset of the queried position inside the item. */
  offsetInItem: number;
}

/**
 * Layout model for a chat list with variable-height items measured on demand.
 * Unmeasured items contribute `estimatedHeight` until `measure` reports a
 * real height. Prefix sums are maintained in a Fenwick tree so offset
 * lookups stay logarithmic.
 */
export class ChatListLayout {
  readonly estimatedHeight: number;

  private keys: string[] = [];
  private heights: number[] = [];
  private keyToIndex = new Map<string, number>();
  private tree = FenwickTree.from([]);

  constructor(options: { estimatedHeight?: number; items?: readonly ChatItem[] } = {}) {
    this.estimatedHeight = options.estimatedHeight ?? 50;
    if (options.items) {
      this.append(options.items);
    }
  }

  get count(): number {
    return this.keys.length;
  }

  totalHeight(): number {
    return this.tree.total();
  }

  keyAt(index: number): string {
    const key = this.keys[index];
    if (key === undefined) {
      throw new RangeError(`index ${index} out of bounds (count ${this.count})`);
    }
    return key;
  }

  indexOfKey(key: string): number {
    return this.keyToIndex.get(key) ?? -1;
  }

  has(key: string): boolean {
    return this.keyToIndex.has(key);
  }

  heightOf(index: number): number {
    const height = this.heights[index];
    if (height === undefined) {
      throw new RangeError(`index ${index} out of bounds (count ${this.count})`);
    }
    return height;
  }

  /** Offset of the item's top edge: sum of all heights before `index`. */
  topOf(index: number): number {
    if (index < 0 || index > this.count) {
      throw new RangeError(`index ${index} out of bounds (count ${this.count})`);
    }
    return this.tree.sum(index);
  }

  bottomOf(index: number): number {
    return this.topOf(index) + this.heightOf(index);
  }

  append(items: readonly ChatItem[]): void {
    this.insert(this.count, items);
  }

  prepend(items: readonly ChatItem[]): void {
    this.insert(0, items);
  }

  insert(index: number, items: readonly ChatItem[]): void {
    if (index < 0 || index > this.count) {
      throw new RangeError(`insert index ${index} out of bounds (count ${this.count})`);
    }
    if (items.length === 0) {
      return;
    }
    const newKeys: string[] = [];
    const newHeights: number[] = [];
    for (const item of items) {
      if (this.keyToIndex.has(item.key)) {
        throw new Error(`duplicate key "${item.key}"`);
      }
      newKeys.push(item.key);
      newHeights.push(item.height ?? this.estimatedHeight);
    }
    this.keys.splice(index, 0, ...newKeys);
    this.heights.splice(index, 0, ...newHeights);
    this.reindex();
  }

  /** Removes `count` items starting at `index`; returns the removed keys. */
  removeAt(index: number, count = 1): string[] {
    if (index < 0 || index + count > this.count) {
      throw new RangeError(
        `remove range [${index}, ${index + count}) out of bounds (count ${this.count})`,
      );
    }
    const removed = this.keys.splice(index, count);
    this.heights.splice(index, count);
    this.reindex();
    return removed;
  }

  /** Removes the given keys; returns the keys that were actually present. */
  removeKeys(keys: readonly string[]): string[] {
    const toRemove = new Set<string>();
    for (const key of keys) {
      if (this.keyToIndex.has(key)) {
        toRemove.add(key);
      }
    }
    if (toRemove.size === 0) {
      return [];
    }
    const keptKeys: string[] = [];
    const keptHeights: number[] = [];
    for (let i = 0; i < this.keys.length; i++) {
      const key = this.keys[i]!;
      if (!toRemove.has(key)) {
        keptKeys.push(key);
        keptHeights.push(this.heights[i]!);
      }
    }
    this.keys = keptKeys;
    this.heights = keptHeights;
    this.reindex();
    return [...toRemove];
  }

  /**
   * Records the measured height of an item (height correction).
   * Returns the delta applied, or 0 when the key is unknown or unchanged.
   */
  measure(key: string, height: number): number {
    if (height < 0) {
      throw new RangeError(`height must be non-negative, got ${height}`);
    }
    const index = this.indexOfKey(key);
    if (index < 0) {
      return 0;
    }
    const delta = height - this.heights[index]!;
    if (delta !== 0) {
      this.heights[index] = height;
      this.tree.add(index, delta);
    }
    return delta;
  }

  /**
   * Locates the item containing `offset`. An offset exactly on a boundary
   * belongs to the item starting there. Out-of-range offsets are clamped
   * to the list. Returns null for an empty list.
   */
  locate(offset: number): ItemLocation | null {
    if (this.count === 0) {
      return null;
    }
    const clamped = Math.min(Math.max(offset, 0), this.totalHeight());
    const index = Math.min(this.tree.maxPrefixLeq(clamped), this.count - 1);
    const top = this.tree.sum(index);
    return {
      index,
      key: this.keys[index]!,
      top,
      height: this.heights[index]!,
      offsetInItem: clamped - top,
    };
  }

  private reindex(): void {
    this.keyToIndex = new Map(this.keys.map((key, index) => [key, index]));
    this.tree = FenwickTree.from(this.heights);
  }
}
