import type { Anchor, BatchOp, ItemId } from '../src/layout.ts';

export class RefLayout {
  private ids: ItemId[] = [];
  private heights: number[] = [];
  private measured = new Set<ItemId>();
  private readonly estimate: number;

  constructor(estimate: number) {
    this.estimate = estimate;
  }

  get count(): number {
    return this.ids.length;
  }

  prefixSums(): number[] {
    const prefix = new Array<number>(this.ids.length + 1);
    prefix[0] = 0;
    for (let i = 0; i < this.ids.length; i++) {
      prefix[i + 1] = prefix[i] + this.heights[i];
    }
    return prefix;
  }

  totalHeight(): number {
    return this.prefixSums()[this.ids.length];
  }

  isMeasured(id: ItemId): boolean {
    return this.measured.has(id);
  }

  heightOf(id: ItemId): number {
    return this.heights[this.ids.indexOf(id)];
  }

  offsetOf(id: ItemId): number {
    return this.prefixSums()[this.ids.indexOf(id)];
  }

  locate(offset: number): { id: ItemId | null; offsetIn: number } {
    if (this.ids.length === 0) return { id: null, offsetIn: 0 };
    const prefix = this.prefixSums();
    const total = prefix[this.ids.length];
    const clamped = Math.min(Math.max(offset, 0), total);
    for (let i = 0; i < this.ids.length; i++) {
      if (prefix[i + 1] > clamped) {
        return { id: this.ids[i], offsetIn: clamped - prefix[i] };
      }
    }
    const last = this.ids.length - 1;
    return { id: this.ids[last], offsetIn: this.heights[last] };
  }

  captureAnchor(scrollOffset: number): Anchor | null {
    const { id, offsetIn } = this.locate(scrollOffset);
    return id === null ? null : { id, offsetIn };
  }

  applyBatch(ops: BatchOp[], anchor?: Anchor | null): number | undefined {
    let target: { id: ItemId; offsetIn: number; toBottom: boolean } | null = null;
    if (anchor) {
      const removed = new Set<ItemId>();
      for (const op of ops) {
        if (op.type === 'remove') for (const id of op.ids) removed.add(id);
      }
      const index = this.ids.indexOf(anchor.id);
      if (index >= 0) {
        if (!removed.has(anchor.id)) {
          target = { id: anchor.id, offsetIn: anchor.offsetIn, toBottom: false };
        } else {
          for (let i = index + 1; i < this.ids.length && !target; i++) {
            if (!removed.has(this.ids[i])) {
              target = { id: this.ids[i], offsetIn: 0, toBottom: false };
            }
          }
          for (let i = index - 1; i >= 0 && !target; i--) {
            if (!removed.has(this.ids[i])) {
              target = { id: this.ids[i], offsetIn: 0, toBottom: true };
            }
          }
        }
      }
    }
    for (const op of ops) this.applyOp(op);
    if (!anchor || !target) return undefined;
    const index = this.ids.indexOf(target.id);
    if (index < 0) return undefined;
    const height = this.heights[index];
    const within = target.toBottom
      ? height
      : Math.min(Math.max(target.offsetIn, 0), height);
    return this.prefixSums()[index] + within;
  }

  update(ops: BatchOp[], scrollOffset: number): number {
    const anchor = this.captureAnchor(scrollOffset);
    const adjusted = this.applyBatch(ops, anchor);
    if (adjusted !== undefined) return adjusted;
    return Math.min(Math.max(scrollOffset, 0), this.totalHeight());
  }

  private applyOp(op: BatchOp): void {
    switch (op.type) {
      case 'prepend':
        this.insertAt(0, op.items.map((item) => item.id));
        break;
      case 'insert':
        this.insertAt(op.index, op.items.map((item) => item.id));
        break;
      case 'remove': {
        const removed = new Set(op.ids);
        const keptIds: ItemId[] = [];
        const keptHeights: number[] = [];
        for (let i = 0; i < this.ids.length; i++) {
          if (removed.has(this.ids[i])) continue;
          keptIds.push(this.ids[i]);
          keptHeights.push(this.heights[i]);
        }
        this.ids = keptIds;
        this.heights = keptHeights;
        for (const id of removed) this.measured.delete(id);
        break;
      }
      case 'measure': {
        const index = this.ids.indexOf(op.id);
        if (index >= 0) {
          this.heights[index] = op.height;
          this.measured.add(op.id);
        }
        break;
      }
      case 'unmeasure': {
        const index = this.ids.indexOf(op.id);
        if (index >= 0) {
          this.heights[index] = this.estimate;
          this.measured.delete(op.id);
        }
        break;
      }
    }
  }

  private insertAt(index: number, ids: ItemId[]): void {
    const at = Math.max(0, Math.min(index, this.ids.length));
    this.ids.splice(at, 0, ...ids);
    this.heights.splice(at, 0, ...ids.map(() => this.estimate));
  }
}
