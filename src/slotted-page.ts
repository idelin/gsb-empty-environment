/**
 * SlottedPage —— 紧凑可变长字节槽位页管理器。
 *
 * 页内布局（默认 4KB）：
 *
 *   0                        freeStart        freeEnd                 pageSize
 *   ┌──────────┬──────────────┬~~~~~~~~~~~~~~~┬───────────────────────┐
 *   │ 页头      │ 槽位数组 →→→ │   连续空闲空间  │ ←←← 记录数据（向页尾对齐）│
 *   └──────────┴──────────────┴~~~~~~~~~~~~~~~┴───────────────────────┘
 *
 * - 页头：magic / slotCount / freeStart / freeEnd / tombstoneCount
 * - 槽位数组从页头之后向前增长，每项 5 字节：offset(u16) length(u16) flags(u8)
 * - 实际记录字节从页尾向页前填充
 * - 删除仅打 tombstone 标记（产生页内碎片），compact() 将存活记录
 *   重新向页尾对齐、重写槽位偏移，使空闲空间收缩为一段连续区间
 */

export const PAGE_SIZE = 4096;

const MAGIC = 0x5350; // "SP"
const HEADER_SIZE = 10;
const SLOT_SIZE = 5;

const OFF_MAGIC = 0;
const OFF_SLOT_COUNT = 2;
const OFF_FREE_START = 4;
const OFF_FREE_END = 6;
const OFF_TOMBSTONES = 8;

const SLOT_OFFSET = 0;
const SLOT_LENGTH = 2;
const SLOT_FLAGS = 4;

const FLAG_DELETED = 0x1;

export class PageFullError extends Error {
  constructor(needed: number, available: number) {
    super(`page full: need ${needed} bytes, only ${available} bytes available`);
    this.name = "PageFullError";
  }
}

export class InvalidSlotError extends Error {
  constructor(slotId: number) {
    super(`invalid slot id: ${slotId}`);
    this.name = "InvalidSlotError";
  }
}

export class RecordTooLargeError extends Error {
  constructor(size: number, max: number) {
    super(`record of ${size} bytes exceeds max record size ${max}`);
    this.name = "RecordTooLargeError";
  }
}

export class SlottedPage {
  readonly pageSize: number;
  private readonly buf: Buffer;

  constructor(pageSize: number = PAGE_SIZE, existing?: Buffer) {
    if (pageSize > 0xffff + 1) {
      throw new Error("pageSize must fit in 16-bit offsets");
    }
    this.pageSize = pageSize;
    if (existing) {
      if (existing.length !== pageSize) {
        throw new Error(`buffer size ${existing.length} != pageSize ${pageSize}`);
      }
      if (existing.readUInt16LE(OFF_MAGIC) !== MAGIC) {
        throw new Error("buffer is not a slotted page (bad magic)");
      }
      this.buf = existing;
    } else {
      this.buf = Buffer.alloc(pageSize);
      this.buf.writeUInt16LE(MAGIC, OFF_MAGIC);
      this.buf.writeUInt16LE(0, OFF_SLOT_COUNT);
      this.buf.writeUInt16LE(HEADER_SIZE, OFF_FREE_START);
      this.buf.writeUInt16LE(pageSize, OFF_FREE_END);
      this.buf.writeUInt16LE(0, OFF_TOMBSTONES);
    }
  }

  /** 底层页字节（可用于持久化）。 */
  get bytes(): Buffer {
    return this.buf;
  }

  get slotCount(): number {
    return this.buf.readUInt16LE(OFF_SLOT_COUNT);
  }

  get tombstoneCount(): number {
    return this.buf.readUInt16LE(OFF_TOMBSTONES);
  }

  get liveCount(): number {
    return this.slotCount - this.tombstoneCount;
  }

  /** 槽位数组末端（空闲区下界）。 */
  private get freeStart(): number {
    return this.buf.readUInt16LE(OFF_FREE_START);
  }

  private set freeStart(v: number) {
    this.buf.writeUInt16LE(v, OFF_FREE_START);
  }

  /** 数据区起点（空闲区上界）。 */
  private get freeEnd(): number {
    return this.buf.readUInt16LE(OFF_FREE_END);
  }

  private set freeEnd(v: number) {
    this.buf.writeUInt16LE(v, OFF_FREE_END);
  }

  /** 槽位数组与数据区之间的连续空闲字节数。 */
  get contiguousFree(): number {
    return this.freeEnd - this.freeStart;
  }

  /** 被删除记录占据、待 compact 回收的碎片字节数。 */
  get fragmentedFree(): number {
    let total = 0;
    for (let i = 0; i < this.slotCount; i++) {
      if (this.isDeleted(i)) total += this.slotLength(i);
    }
    return total;
  }

  /** 总可用空间（连续空闲 + 可整理回收的碎片）。 */
  get totalFree(): number {
    return this.contiguousFree + this.fragmentedFree;
  }

  /** 空间利用率：存活数据 / (页大小 - 页头)。 */
  get utilization(): number {
    let live = 0;
    for (let i = 0; i < this.slotCount; i++) {
      if (!this.isDeleted(i)) live += this.slotLength(i);
    }
    return live / (this.pageSize - HEADER_SIZE);
  }

  private slotAddr(slotId: number): number {
    if (slotId < 0 || slotId >= this.slotCount) throw new InvalidSlotError(slotId);
    return HEADER_SIZE + slotId * SLOT_SIZE;
  }

  private slotOffset(slotId: number): number {
    return this.buf.readUInt16LE(this.slotAddr(slotId) + SLOT_OFFSET);
  }

  private slotLength(slotId: number): number {
    return this.buf.readUInt16LE(this.slotAddr(slotId) + SLOT_LENGTH);
  }

  private isDeleted(slotId: number): boolean {
    return (this.buf.readUInt8(this.slotAddr(slotId) + SLOT_FLAGS) & FLAG_DELETED) !== 0;
  }

  /**
   * 插入一条可变长记录，返回槽位 id。
   * 优先复用 tombstone 槽位；连续空间不足但总空间足够时自动触发 compact()。
   */
  insert(record: string | Buffer): number {
    const data = typeof record === "string" ? Buffer.from(record, "utf8") : record;
    const maxRecord = this.pageSize - HEADER_SIZE - SLOT_SIZE;
    if (data.length > maxRecord) {
      throw new RecordTooLargeError(data.length, maxRecord);
    }

    // 查找可复用的 tombstone 槽位
    let slotId = -1;
    for (let i = 0; i < this.slotCount; i++) {
      if (this.isDeleted(i)) {
        slotId = i;
        break;
      }
    }
    const needNewSlot = slotId === -1;
    const needed = data.length + (needNewSlot ? SLOT_SIZE : 0);

    if (this.contiguousFree < needed) {
      if (this.totalFree < needed) throw new PageFullError(needed, this.totalFree);
      this.compact();
      if (this.contiguousFree < needed) {
        throw new PageFullError(needed, this.contiguousFree);
      }
    }

    // 数据写入页尾方向
    const dataOffset = this.freeEnd - data.length;
    data.copy(this.buf, dataOffset);
    this.freeEnd = dataOffset;

    if (needNewSlot) {
      slotId = this.slotCount;
      const addr = HEADER_SIZE + slotId * SLOT_SIZE;
      this.buf.writeUInt16LE(dataOffset, addr + SLOT_OFFSET);
      this.buf.writeUInt16LE(data.length, addr + SLOT_LENGTH);
      this.buf.writeUInt8(0, addr + SLOT_FLAGS);
      this.buf.writeUInt16LE(slotId + 1, OFF_SLOT_COUNT);
      this.freeStart = HEADER_SIZE + (slotId + 1) * SLOT_SIZE;
    } else {
      const addr = this.slotAddr(slotId);
      this.buf.writeUInt16LE(dataOffset, addr + SLOT_OFFSET);
      this.buf.writeUInt16LE(data.length, addr + SLOT_LENGTH);
      this.buf.writeUInt8(0, addr + SLOT_FLAGS);
      this.buf.writeUInt16LE(this.tombstoneCount - 1, OFF_TOMBSTONES);
    }
    return slotId;
  }

  /** 读取记录；槽位不存在或已删除返回 null。 */
  get(slotId: number): Buffer | null {
    if (slotId < 0 || slotId >= this.slotCount || this.isDeleted(slotId)) return null;
    const off = this.slotOffset(slotId);
    const len = this.slotLength(slotId);
    return this.buf.subarray(off, off + len);
  }

  /** 以 UTF-8 字符串读取记录。 */
  getString(slotId: number): string | null {
    const data = this.get(slotId);
    return data === null ? null : data.toString("utf8");
  }

  /** 删除记录：仅打 tombstone 标记，数据留待 compact() 回收。 */
  delete(slotId: number): boolean {
    if (slotId < 0 || slotId >= this.slotCount || this.isDeleted(slotId)) return false;
    const addr = this.slotAddr(slotId);
    this.buf.writeUInt8(this.buf.readUInt8(addr + SLOT_FLAGS) | FLAG_DELETED, addr + SLOT_FLAGS);
    this.buf.writeUInt16LE(this.tombstoneCount + 1, OFF_TOMBSTONES);
    return true;
  }

  /**
   * 页内碎片整理：
   * 1. 按当前偏移从大到小遍历存活记录（保持页尾到页前的物理顺序）；
   * 2. 依次重新向页尾对齐写入；
   * 3. 重写每个槽位的 offset；
   * 4. 空闲空间收缩为槽位数组与数据区之间的一段连续区间。
   */
  compact(): void {
    interface Live {
      slotId: number;
      offset: number;
      length: number;
    }
    const live: Live[] = [];
    for (let i = 0; i < this.slotCount; i++) {
      if (!this.isDeleted(i)) {
        live.push({ slotId: i, offset: this.slotOffset(i), length: this.slotLength(i) });
      }
    }
    live.sort((a, b) => b.offset - a.offset);

    // 先拷贝到暂存区，避免原地移动时相互覆盖
    const staging = Buffer.alloc(this.pageSize - HEADER_SIZE);
    let cursor = this.pageSize;
    for (const rec of live) {
      cursor -= rec.length;
      this.buf.copy(staging, cursor - HEADER_SIZE, rec.offset, rec.offset + rec.length);
      const addr = this.slotAddr(rec.slotId);
      this.buf.writeUInt16LE(cursor, addr + SLOT_OFFSET);
    }
    staging.copy(this.buf, cursor, cursor - HEADER_SIZE, this.pageSize - HEADER_SIZE);

    // 已删除槽位占用的空间已回收，清零其长度使碎片统计归零
    for (let i = 0; i < this.slotCount; i++) {
      if (this.isDeleted(i)) {
        const addr = this.slotAddr(i);
        this.buf.writeUInt16LE(0, addr + SLOT_OFFSET);
        this.buf.writeUInt16LE(0, addr + SLOT_LENGTH);
      }
    }

    this.freeEnd = cursor;
  }

  /** 清空页（保留页大小）。 */
  reset(): void {
    this.buf.writeUInt16LE(0, OFF_SLOT_COUNT);
    this.freeStart = HEADER_SIZE;
    this.freeEnd = this.pageSize;
    this.buf.writeUInt16LE(0, OFF_TOMBSTONES);
  }

  /** 页状态摘要，用于调试与演示。 */
  stats() {
    return {
      pageSize: this.pageSize,
      slotCount: this.slotCount,
      liveCount: this.liveCount,
      tombstoneCount: this.tombstoneCount,
      contiguousFree: this.contiguousFree,
      fragmentedFree: this.fragmentedFree,
      totalFree: this.totalFree,
      utilization: Number(this.utilization.toFixed(4)),
    };
  }
}
