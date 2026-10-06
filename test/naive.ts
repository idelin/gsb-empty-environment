import { captureAnchor, resolveAnchor } from "../src/anchor.ts";
import type { ChatItem, ItemLocation } from "../src/layout.ts";

/**
 * Reference model: a deliberately naive layout that recomputes full prefix
 * sums from a plain array on every query. Used to cross-check the
 * Fenwick-tree based ChatListLayout.
 */
export class NaiveLayout {
  readonly estimatedHeight: number;
  private entries: { key: string; height: number }[] = [];

  constructor(options: { estimatedHeight?: number } = {}) {
    this.estimatedHeight = options.estimatedHeight ?? 50;
  }

  get count(): number {
    return this.entries.length;
  }

  /** Full prefix sums: prefix[i] = sum of the first i heights. */
  prefixes(): number[] {
    const prefix = [0];
    for (const entry of this.entries) {
      prefix.push(prefix[prefix.length - 1]! + entry.height);
    }
    return prefix;
  }

  totalHeight(): number {
    return this.entries.reduce((sum, entry) => sum + entry.height, 0);
  }

  keyAt(index: number): string {
    const entry = this.entries[index];
    if (!entry) {
      throw new RangeError(`index ${index} out of bounds (count ${this.count})`);
    }
    return entry.key;
  }

  indexOfKey(key: string): number {
    return this.entries.findIndex((entry) => entry.key === key);
  }

  has(key: string): boolean {
    return this.indexOfKey(key) >= 0;
  }

  heightOf(index: number): number {
    const entry = this.entries[index];
    if (!entry) {
      throw new RangeError(`index ${index} out of bounds (count ${this.count})`);
    }
    return entry.height;
  }

  topOf(index: number): number {
    if (index < 0 || index > this.count) {
      throw new RangeError(`index ${index} out of bounds (count ${this.count})`);
    }
    let top = 0;
    for (let i = 0; i < index; i++) {
      top += this.entries[i]!.height;
    }
    return top;
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
    const entries = items.map((item) => {
      if (this.has(item.key)) {
        throw new Error(`duplicate key "${item.key}"`);
      }
      return { key: item.key, height: item.height ?? this.estimatedHeight };
    });
    this.entries.splice(index, 0, ...entries);
  }

  removeKeys(keys: readonly string[]): string[] {
    const toRemove = new Set(keys.filter((key) => this.has(key)));
    this.entries = this.entries.filter((entry) => !toRemove.has(entry.key));
    return [...toRemove];
  }

  measure(key: string, height: number): number {
    const index = this.indexOfKey(key);
    if (index < 0) {
      return 0;
    }
    const delta = height - this.entries[index]!.height;
    this.entries[index]!.height = height;
    return delta;
  }

  locate(offset: number): ItemLocation | null {
    if (this.count === 0) {
      return null;
    }
    const clamped = Math.min(Math.max(offset, 0), this.totalHeight());
    const prefix = this.prefixes();
    let index = 0;
    for (let i = 0; i < this.count; i++) {
      if (prefix[i]! <= clamped) {
        index = i;
      }
    }
    return {
      index,
      key: this.entries[index]!.key,
      top: prefix[index]!,
      height: this.entries[index]!.height,
      offsetInItem: clamped - prefix[index]!,
    };
  }
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

/** Reference viewport implementing the same anchoring policy naively. */
export class NaiveViewport {
  readonly layout: NaiveLayout;
  viewportHeight: number;
  private currentOffset = 0;

  constructor(layout: NaiveLayout, options: { viewportHeight: number }) {
    this.layout = layout;
    this.viewportHeight = options.viewportHeight;
  }

  get scrollOffset(): number {
    return this.currentOffset;
  }

  get maxOffset(): number {
    return Math.max(0, this.layout.totalHeight() - this.viewportHeight);
  }

  scrollTo(offset: number): number {
    this.currentOffset = clamp(offset, 0, this.maxOffset);
    return this.currentOffset;
  }

  prepend(items: readonly ChatItem[]): void {
    this.transact(() => this.layout.prepend(items));
  }

  append(items: readonly ChatItem[]): void {
    this.transact(() => this.layout.append(items));
  }

  insert(index: number, items: readonly ChatItem[]): void {
    this.transact(() => this.layout.insert(index, items));
  }

  removeKeys(keys: readonly string[]): string[] {
    let removed: string[] = [];
    this.transact(() => {
      removed = this.layout.removeKeys(keys);
    });
    return removed;
  }

  measure(key: string, height: number): number {
    let delta = 0;
    this.transact(() => {
      delta = this.layout.measure(key, height);
    });
    return delta;
  }

  private transact(mutate: () => void): void {
    const anchor = captureAnchor(this.layout, this.currentOffset);
    mutate();
    this.currentOffset = clamp(resolveAnchor(this.layout, anchor), 0, this.maxOffset);
  }
}
