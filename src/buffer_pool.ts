/**
 * 缓冲池（Buffer Pool）与磁盘管理器（Disk Manager）模拟实现。
 *
 * - DiskManager：模拟持久化存储，页面写入时深拷贝（相当于序列化到磁盘）。
 * - BufferPool：固定容量帧（frame），LRU 置换；只淘汰 pinCount === 0 的页。
 *   fetchPage 会 Pin 页面，unpinPage 解除 Pin；所有帧都被 Pin 住时抛错，
 *   因此任何 Pin 泄漏都会立刻暴露（表现为缓冲池耗尽）。
 */

export class DiskManager {
  private storage = new Map<number, unknown>();
  private nextPageId = 1;

  allocatePageId(): number {
    return this.nextPageId++;
  }

  writePage<T>(pageId: number, data: T): void {
    this.storage.set(pageId, structuredClone(data));
  }

  readPage<T>(pageId: number): T {
    const data = this.storage.get(pageId);
    if (data === undefined) {
      throw new Error(`DiskManager: page ${pageId} does not exist`);
    }
    return structuredClone(data) as T;
  }
}

interface Frame {
  pageId: number;
  data: unknown;
  pinCount: number;
  dirty: boolean;
  lastUsed: number;
}

export class BufferPool {
  private readonly disk: DiskManager;
  public readonly capacity: number;
  private frames = new Map<number, Frame>();
  private clock = 0;

  /** 统计：累计 fetch 次数（含命中），用于观察扫描期间的页面读取行为。 */
  public fetchCount = 0;
  /** 统计：发生磁盘读取（未命中）的次数。 */
  public diskReadCount = 0;

  constructor(disk: DiskManager, capacity: number) {
    if (capacity < 2) throw new Error('BufferPool capacity must be >= 2');
    this.disk = disk;
    this.capacity = capacity;
  }

  /** 分配新页，返回时处于 Pinned 状态，调用方负责 unpinPage。 */
  newPage<T>(initialData: T): { pageId: number; data: T } {
    const pageId = this.disk.allocatePageId();
    const frame = this.obtainFrame();
    frame.pageId = pageId;
    frame.data = structuredClone(initialData);
    frame.pinCount = 1;
    frame.dirty = true;
    frame.lastUsed = ++this.clock;
    this.frames.set(pageId, frame);
    return { pageId, data: frame.data as T };
  }

  /** 取页并 Pin。返回的引用在 unpin 之前保证驻留内存。 */
  fetchPage<T>(pageId: number): T {
    this.fetchCount++;
    let frame = this.frames.get(pageId);
    if (!frame) {
      this.diskReadCount++;
      frame = this.obtainFrame();
      frame.pageId = pageId;
      frame.data = this.disk.readPage(pageId);
      frame.pinCount = 0;
      frame.dirty = false;
      this.frames.set(pageId, frame);
    }
    frame.pinCount++;
    frame.lastUsed = ++this.clock;
    return frame.data as T;
  }

  /** 解除 Pin；isDirty 标记页面被修改过，淘汰时需写回磁盘。 */
  unpinPage(pageId: number, isDirty = false): void {
    const frame = this.frames.get(pageId);
    if (!frame) {
      throw new Error(`BufferPool: unpin of non-resident page ${pageId}`);
    }
    if (frame.pinCount <= 0) {
      throw new Error(`BufferPool: unpin of already-unpinned page ${pageId}`);
    }
    frame.pinCount--;
    frame.dirty = frame.dirty || isDirty;
    frame.lastUsed = ++this.clock;
  }

  /** 当前被 Pin 住的页面总数；迭代器正常结束后应为 0，否则即为泄漏。 */
  get pinnedCount(): number {
    let total = 0;
    for (const frame of this.frames.values()) total += frame.pinCount;
    return total;
  }

  get residentPages(): number {
    return this.frames.size;
  }

  flushAll(): void {
    for (const frame of this.frames.values()) {
      if (frame.dirty) {
        this.disk.writePage(frame.pageId, frame.data);
        frame.dirty = false;
      }
    }
  }

  /** 取一个空闲帧；池满时按 LRU 淘汰未 Pin 的页，全部被 Pin 则抛错。 */
  private obtainFrame(): Frame {
    if (this.frames.size < this.capacity) {
      return { pageId: -1, data: null, pinCount: 0, dirty: false, lastUsed: 0 };
    }
    let victim: Frame | undefined;
    for (const frame of this.frames.values()) {
      if (frame.pinCount === 0 && (!victim || frame.lastUsed < victim.lastUsed)) {
        victim = frame;
      }
    }
    if (!victim) {
      throw new Error(
        'BufferPool exhausted: every frame is pinned (possible pin leak)',
      );
    }
    if (victim.dirty) {
      this.disk.writePage(victim.pageId, victim.data);
    }
    this.frames.delete(victim.pageId);
    return { pageId: -1, data: null, pinCount: 0, dirty: false, lastUsed: 0 };
  }
}
