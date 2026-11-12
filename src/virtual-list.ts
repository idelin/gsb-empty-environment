import type { BusinessKey, ScrollSnapshot, VisibleRow } from "./types.ts";

export interface VirtualItem {
  key: BusinessKey;
  content: unknown;
  renderGeneration: number;
}

export interface NodeBindingEvent {
  readonly round: number;
  readonly nodeId: string;
  readonly fromKey: BusinessKey | null;
  readonly toKey: BusinessKey | null;
}

export interface VirtualListOptions {
  /** 视口最多渲染的 DOM 节点数（overscan 之外的固定节点池大小）。 */
  readonly nodeCount: number;
  /** 为 true 时越过末尾会回到头部，模拟循环/无限列表。 */
  readonly circular?: boolean;
}

/**
 * 最小虚拟列表模型：只维护 nodeCount 个可复用节点，滚动时节点槽位保持稳定，
 * 槽位上承载的业务键随窗口变化——以此真实复现 DOM 回收复用。
 */
export class VirtualList {
  #items: VirtualItem[];
  readonly #nodeCount: number;
  readonly #circular: boolean;
  #topIndex = 0;
  #round = 0;
  #endMarked = false;
  /** 每个节点槽位当前承载的键，null 表示空槽。 */
  readonly #slotKeys: (BusinessKey | null)[];
  readonly #bindingLog: NodeBindingEvent[] = [];

  constructor(initialItems: ReadonlyArray<VirtualItem>, options: VirtualListOptions) {
    this.#items = initialItems.map((item) => ({ ...item }));
    this.#nodeCount = options.nodeCount;
    this.#circular = options.circular ?? false;
    this.#slotKeys = new Array(options.nodeCount).fill(null);
  }

  get topIndex(): number {
    return this.#topIndex;
  }

  get round(): number {
    return this.#round;
  }

  get length(): number {
    return this.#items.length;
  }

  allKeys(): BusinessKey[] {
    return this.#items.map((item) => item.key);
  }

  item(key: BusinessKey): VirtualItem | undefined {
    return this.#items.find((candidate) => candidate.key === key);
  }

  bindingLog(): ReadonlyArray<NodeBindingEvent> {
    return this.#bindingLog.map((event) => ({ ...event }));
  }

  markEnd(marked = true): void {
    this.#endMarked = marked;
  }

  /** 滚动使 key 所在行成为视口第一行。 */
  scrollToKey(key: BusinessKey): void {
    const index = this.#items.findIndex((item) => item.key === key);
    if (index >= 0) this.scrollToIndex(index);
  }

  /** 相对当前窗口滚动 delta 行（负数回滚）。 */
  scrollBy(delta: number): void {
    this.scrollToIndex(this.#topIndex + delta);
  }

  scrollToIndex(index: number): void {
    const total = this.#items.length;
    let target = index;
    if (this.#circular && total > 0) {
      target = ((index % total) + total) % total;
    } else {
      target = Math.max(0, Math.min(index, Math.max(0, total - 1)));
    }
    this.#topIndex = target;
  }

  /** 在列表头部插入一行：模拟实时数据插队。 */
  insertAtHead(item: VirtualItem): void {
    this.#items = [{ ...item }, ...this.#items];
    // 视口锚定在当前首行：新行位于视口上方，需向上滚动才会进入视口，
    // 因此 topIndex 保持不变（其指向的业务键已整体后移一位）。
  }

  /** 更新某键内容并推进渲染代次；默认浅合并，replace 为 true 时整体替换。 */
  updateContent(
    key: BusinessKey,
    patch: Record<string, unknown>,
    replace = false,
  ): void {
    const item = this.#items.find((candidate) => candidate.key === key);
    if (!item) return;
    const current =
      replace || typeof item.content !== "object" || item.content === null
        ? patch
        : { ...(item.content as Record<string, unknown>), ...patch };
    item.content = current;
    item.renderGeneration += 1;
  }

  /** 产出当前窗口的可见行快照，并记录节点换绑事件。 */
  snapshot(): ScrollSnapshot {
    const total = this.#items.length;
    const rows: VisibleRow[] = [];
    const nextSlotKeys: (BusinessKey | null)[] = new Array(this.#nodeCount).fill(null);

    for (let slot = 0; slot < this.#nodeCount; slot += 1) {
      let itemIndex: number | null = null;
      if (total > 0) {
        const candidate = this.#topIndex + slot;
        if (this.#circular) {
          itemIndex = candidate % total;
        } else if (candidate < total) {
          itemIndex = candidate;
        }
      }

      const nodeId = `node-${slot}`;
      if (itemIndex === null) {
        if (this.#slotKeys[slot] !== null) {
          this.#bindingLog.push({
            round: this.#round,
            nodeId,
            fromKey: this.#slotKeys[slot],
            toKey: null,
          });
        }
        continue;
      }

      const item = this.#items[itemIndex];
      nextSlotKeys[slot] = item.key;
      if (this.#slotKeys[slot] !== item.key) {
        this.#bindingLog.push({
          round: this.#round,
          nodeId,
          fromKey: this.#slotKeys[slot],
          toKey: item.key,
        });
      }
      rows.push({
        nodeId,
        key: item.key,
        content: item.content,
        renderGeneration: item.renderGeneration,
      });
    }

    for (let slot = 0; slot < this.#nodeCount; slot += 1) {
      this.#slotKeys[slot] = nextSlotKeys[slot];
    }

    const snapshot: ScrollSnapshot = {
      round: this.#round,
      rows,
      endMarked: this.#endMarked,
    };
    this.#round += 1;
    return snapshot;
  }
}
