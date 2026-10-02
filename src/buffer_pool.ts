import { Page, type NodeData } from "./page.ts";

// 简化版缓冲池：固定数量的页框，LRU 置换。
// - fetchPage / newPage 返回的页处于 Pinned 状态，用完必须 unpinPage。
// - 仅当某页 pinCount 降为 0 时才可被置换。
// - 若所有页框都被 Pin 住且还需要新页，抛出异常（调用方存在 Pin 泄漏）。
export class BufferPool {
  readonly capacity: number;
  // “磁盘”：全量页的持久化存储（此处用内存模拟）
  private disk: Map<number, Page> = new Map();
  // 内存中的页框
  private frames: Map<number, Page> = new Map();
  private pinCounts: Map<number, number> = new Map();
  // 未 Pin 页框的 LRU 顺序（Map 的插入序，越靠前越旧）
  private lruUnpinned: Map<number, true> = new Map();
  private nextPageId = 0;

  // 统计信息，供测试与演示观察
  fetchCount = 0;
  evictionCount = 0;

  constructor(capacity: number) {
    if (capacity < 2) {
      throw new Error("BufferPool capacity must be >= 2");
    }
    this.capacity = capacity;
  }

  allocatePageId(): number {
    return this.nextPageId++;
  }

  // 新建一页并写入“磁盘”，返回已 Pin 的页
  newPage(node: NodeData): Page {
    const pageId = this.allocatePageId();
    const page = new Page(pageId, node);
    page.dirty = true;
    this.disk.set(pageId, page);
    this.loadIntoFrame(page);
    return page;
  }

  // 取页并 Pin 住
  fetchPage(pageId: number): Page {
    this.fetchCount++;
    const cached = this.frames.get(pageId);
    if (cached !== undefined) {
      this.pin(cached);
      return cached;
    }
    const stored = this.disk.get(pageId);
    if (stored === undefined) {
      throw new Error(`page ${pageId} does not exist`);
    }
    this.loadIntoFrame(stored);
    return stored;
  }

  unpinPage(pageId: number): void {
    const count = this.pinCounts.get(pageId);
    if (count === undefined || count <= 0) {
      throw new Error(`page ${pageId} is not pinned`);
    }
    if (count === 1) {
      this.pinCounts.delete(pageId);
      this.lruUnpinned.set(pageId, true);
    } else {
      this.pinCounts.set(pageId, count - 1);
    }
  }

  isPinned(pageId: number): boolean {
    return this.pinCounts.has(pageId);
  }

  // 当前被 Pin 住的页总数（测试用它验证无泄漏）
  get totalPins(): number {
    let total = 0;
    for (const count of this.pinCounts.values()) {
      total += count;
    }
    return total;
  }

  get frameCount(): number {
    return this.frames.size;
  }

  private pin(page: Page): void {
    const pageId = page.pageId;
    const count = this.pinCounts.get(pageId) ?? 0;
    this.pinCounts.set(pageId, count + 1);
    this.lruUnpinned.delete(pageId);
  }

  private loadIntoFrame(page: Page): void {
    if (this.frames.size >= this.capacity) {
      this.evictOne();
    }
    this.frames.set(page.pageId, page);
    this.pin(page);
  }

  private evictOne(): void {
    const oldest = this.lruUnpinned.keys().next();
    if (oldest.done) {
      throw new Error(
        "BufferPool exhausted: every frame is pinned (possible pin leak)",
      );
    }
    const victimId = oldest.value;
    this.lruUnpinned.delete(victimId);
    this.frames.delete(victimId);
    this.evictionCount++;
    // “写回磁盘”在本实现中是 no-op：disk 与 frames 共享同一 Page 对象。
  }
}
