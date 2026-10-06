import { captureAnchor, resolveAnchor } from "./anchor.ts";
import { ChatListLayout, type ChatItem } from "./layout.ts";

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

/**
 * A viewport over a {@link ChatListLayout}. Every mutation goes through the
 * anchor machinery so the first visible item (and the offset inside it)
 * stays put across prepends, height corrections and removals.
 */
export class ChatViewport {
  readonly layout: ChatListLayout;
  viewportHeight: number;
  private currentOffset = 0;

  constructor(layout: ChatListLayout, options: { viewportHeight: number }) {
    this.layout = layout;
    this.viewportHeight = options.viewportHeight;
  }

  get scrollOffset(): number {
    return this.currentOffset;
  }

  /** Maximum scrollable offset; 0 when the content fits the viewport. */
  get maxOffset(): number {
    return Math.max(0, this.layout.totalHeight() - this.viewportHeight);
  }

  /** Positions the viewport at `offset`, clamped to the scrollable range. */
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
