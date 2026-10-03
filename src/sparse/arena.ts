/**
 * CompactArena：内存紧凑分配器。
 *  - 单一 ArrayBuffer 平板（slab），按 2 的幂尺寸分级（size class）管理空闲块
 *  - alloc/free 显式句柄模型；句柄在 defrag 后依然有效（内部重映射）
 *  - defrag() 原地压实存活块，消除外部碎片
 *  - 严格泄漏检测：每次 alloc 计入 live 表，assertNoLeaks() 在存在未释放
 *    句柄时抛出；dispose() 释放全部底层缓冲
 */

const MIN_CLASS = 4; // 16 字节
const MAX_CLASSES = 24; // 到 128 MiB

function classOf(size: number): number {
  let c = MIN_CLASS;
  while (1 << c < size) c++;
  if (c >= MAX_CLASSES + MIN_CLASS) throw new RangeError(`allocation too large: ${size}`);
  return c;
}

interface Block {
  offset: number;
  size: number; // 实际块大小（2 的幂）
}

export class ArenaExhaustedError extends Error {}
export class ArenaLeakError extends Error {
  readonly leaks: number[];
  constructor(leaks: number[]) {
    super(`arena leak: ${leaks.length} handle(s) not freed: [${leaks.slice(0, 8).join(", ")}${leaks.length > 8 ? ", ..." : ""}]`);
    this.name = "ArenaLeakError";
    this.leaks = leaks;
  }
}

export class CompactArena {
  private buf: ArrayBuffer;
  private freeLists: Block[][] = [];
  private live = new Map<number, Block>();
  private nextHandle = 1;
  private bump = 0;
  private disposed = false;
  /** 统计：defrag 搬运字节数、累计分配/释放次数。 */
  public movedBytes = 0;
  public allocCount = 0;
  public freeCount = 0;
  readonly capacity: number;

  constructor(capacity: number) {
    this.capacity = capacity;
    if (capacity <= 0 || (capacity & (capacity - 1)) !== 0) {
      throw new RangeError("capacity must be a positive power of two");
    }
    this.buf = new ArrayBuffer(capacity);
    for (let i = 0; i <= MAX_CLASSES; i++) this.freeLists.push([]);
  }

  get liveCount(): number {
    return this.live.size;
  }

  get usedBytes(): number {
    let n = 0;
    for (const b of this.live.values()) n += b.size;
    return n;
  }

  get freeBytes(): number {
    let n = this.capacity - this.bump;
    for (const list of this.freeLists) for (const b of list) n += b.size;
    return n;
  }

  alloc(size: number): number {
    this.ensureAlive();
    if (size <= 0) throw new RangeError("size must be positive");
    const cls = classOf(size);
    const blockSize = 1 << cls;
    let block = this.freeLists[cls - MIN_CLASS].pop();
    if (!block) {
      if (this.bump + blockSize > this.capacity) {
        this.defrag();
        if (this.bump + blockSize > this.capacity) {
          throw new ArenaExhaustedError(`arena exhausted: need ${blockSize}, free ${this.freeBytes}`);
        }
      }
      block = { offset: this.bump, size: blockSize };
      this.bump += blockSize;
    }
    const handle = this.nextHandle++;
    this.live.set(handle, block);
    this.allocCount++;
    return handle;
  }

  free(handle: number): void {
    this.ensureAlive();
    const block = this.live.get(handle);
    if (!block) throw new Error(`double free or unknown handle ${handle}`);
    this.live.delete(handle);
    this.freeLists[classOf(block.size) - MIN_CLASS].push(block);
    this.freeCount++;
  }

  /** 写入句柄对应块（offset 相对块起始）。 */
  write(handle: number, offset: number, data: Uint8Array): void {
    const block = this.mustGet(handle);
    if (offset < 0 || offset + data.byteLength > block.size) throw new RangeError("write out of bounds");
    new Uint8Array(this.buf, block.offset + offset, data.byteLength).set(data);
  }

  read(handle: number, offset: number, length: number): Uint8Array {
    const block = this.mustGet(handle);
    if (offset < 0 || offset + length > block.size) throw new RangeError("read out of bounds");
    return new Uint8Array(this.buf, block.offset + offset, length);
  }

  private mustGet(handle: number): Block {
    this.ensureAlive();
    const block = this.live.get(handle);
    if (!block) throw new Error(`unknown handle ${handle}`);
    return block;
  }

  /**
   * 压实：把存活块按句柄顺序搬到平板头部，回收尾部与空洞。
   * 句柄保持不变（内部更新 offset），外部无感。
   */
  defrag(): void {
    this.ensureAlive();
    const entries = [...this.live.entries()].sort((a, b) => a[1].offset - b[1].offset);
    const src = new Uint8Array(this.buf);
    const snapshot = src.slice(0, this.bump);
    const dst = new Uint8Array(this.buf);
    let cursor = 0;
    for (const [, block] of entries) {
      if (block.offset !== cursor) {
        dst.set(snapshot.subarray(block.offset, block.offset + block.size), cursor);
        this.movedBytes += block.size;
        block.offset = cursor;
      }
      cursor += block.size;
    }
    // 重建空闲链表：压实后所有空闲空间在尾部，全部清空重新 bump。
    for (const list of this.freeLists) list.length = 0;
    this.bump = cursor;
  }

  /** 严格无泄漏断言：存在未释放句柄即抛错。 */
  assertNoLeaks(): void {
    if (this.live.size > 0) throw new ArenaLeakError([...this.live.keys()]);
  }

  /** 释放底层缓冲；之后任何操作抛错。返回释放的字节数。 */
  dispose(): number {
    const bytes = this.buf.byteLength;
    this.buf = new ArrayBuffer(0);
    for (const list of this.freeLists) list.length = 0;
    this.live.clear();
    this.bump = 0;
    this.disposed = true;
    return bytes;
  }

  private ensureAlive(): void {
    if (this.disposed) throw new Error("arena disposed");
  }
}
