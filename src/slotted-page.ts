/**
 * SlottedPage —— 紧凑可变长字节槽位页管理器。
 *
 * 页布局（固定 4KB）：
 *
 *   0                8                freeStart        freeEnd            4096
 *   ┌────────────┬───────────────────┬────────────────┬──────────────────┐
 *   │ 页头(8B)   │ 槽位数组(向前增长) │  连续空闲空间   │ 记录数据(向后增长) │
 *   └────────────┴───────────────────┴────────────────┴──────────────────┘
 *
 * 页头（8 字节，uint16 LE）：
 *   [0..1] slotCount        槽位总数（含墓碑）
 *   [2..3] freeStart        槽位数组结束位置（空闲区起点）
 *   [4..5] freeEnd          记录数据区起点（空闲区终点）
 *   [6..7] fragmentedBytes  已删除记录遗留的碎片字节数
 *
 * 槽位（每条 4 字节）：offset(uint16) + length(uint16)。
 * 删除采用墓碑标记：offset = 0xFFFF，槽位 id 保持稳定，可在后续插入时复用。
 */

export const PAGE_SIZE = 4096;
export const HEADER_SIZE = 8;
export const SLOT_SIZE = 4;
export const TOMBSTONE_OFFSET = 0xffff;

export class PageFullError extends Error {
  readonly required: number;
  readonly available: number;
  readonly reclaimable: number;

  constructor(required: number, available: number, reclaimable: number) {
    super(
      `page full: need ${required}B, contiguous free ${available}B` +
        (reclaimable > 0 ? `, ${reclaimable}B reclaimable via compact()` : ''),
    );
    this.name = 'PageFullError';
    this.required = required;
    this.available = available;
    this.reclaimable = reclaimable;
  }
}

export interface PageStats {
  pageSize: number;
  slotCount: number;
  liveRecords: number;
  tombstones: number;
  headerBytes: number;
  slotArrayBytes: number;
  recordBytes: number;
  fragmentedBytes: number;
  freeBytes: number;
  utilization: number; // 存活数据 / 页大小
}

export class SlottedPage {
  private readonly buf: Buffer;
  private slotCount = 0;
  private freeEnd = PAGE_SIZE;
  private fragmentedBytes = 0;

  constructor(pageSize: number = PAGE_SIZE) {
    if (pageSize <= HEADER_SIZE || pageSize > 0x10000) {
      throw new RangeError(`invalid page size: ${pageSize}`);
    }
    this.buf = Buffer.alloc(pageSize);
  }

  get pageSize(): number {
    return this.buf.length;
  }

  private get freeStart(): number {
    return HEADER_SIZE + this.slotCount * SLOT_SIZE;
  }

  /** 连续可用空间（空闲区总字节数）。 */
  freeSpace(): number {
    return this.freeEnd - this.freeStart;
  }

  /** 可通过 compact() 回收的碎片字节数。 */
  fragmentedSpace(): number {
    return this.fragmentedBytes;
  }

  private slotOffsetPos(slotId: number): number {
    return HEADER_SIZE + slotId * SLOT_SIZE;
  }

  private isLive(slotId: number): boolean {
    return this.buf.readUInt16LE(this.slotOffsetPos(slotId)) !== TOMBSTONE_OFFSET;
  }

  /**
   * 插入一条可变长记录，返回槽位 id。
   * 优先复用墓碑槽位；空间不足时抛出 PageFullError（可先 compact() 再重试）。
   */
  insert(data: Buffer | Uint8Array | string): number {
    const bytes = typeof data === 'string' ? Buffer.from(data, 'utf8') : data;
    if (bytes.length === 0) throw new RangeError('empty record');
    if (bytes.length > TOMBSTONE_OFFSET - 1) {
      throw new RangeError(`record too large: ${bytes.length}B`);
    }

    let slotId = -1;
    for (let i = 0; i < this.slotCount; i++) {
      if (!this.isLive(i)) {
        slotId = i;
        break;
      }
    }
    const needSlotBytes = slotId === -1 ? SLOT_SIZE : 0;
    const required = bytes.length + needSlotBytes;
    if (required > this.freeSpace()) {
      throw new PageFullError(required, this.freeSpace(), this.fragmentedBytes);
    }

    if (slotId === -1) {
      slotId = this.slotCount++;
    }
    this.freeEnd -= bytes.length;
    this.buf.set(bytes, this.freeEnd);
    this.buf.writeUInt16LE(this.freeEnd, this.slotOffsetPos(slotId));
    this.buf.writeUInt16LE(bytes.length, this.slotOffsetPos(slotId) + 2);
    return slotId;
  }

  /** 读取记录；槽位不存在或已删除时返回 null。 */
  get(slotId: number): Buffer | null {
    if (slotId < 0 || slotId >= this.slotCount || !this.isLive(slotId)) return null;
    const pos = this.slotOffsetPos(slotId);
    const offset = this.buf.readUInt16LE(pos);
    const length = this.buf.readUInt16LE(pos + 2);
    return this.buf.subarray(offset, offset + length);
  }

  /** 删除记录：槽位置为墓碑，记录字节计入碎片。 */
  delete(slotId: number): boolean {
    if (slotId < 0 || slotId >= this.slotCount || !this.isLive(slotId)) return false;
    const pos = this.slotOffsetPos(slotId);
    this.fragmentedBytes += this.buf.readUInt16LE(pos + 2);
    this.buf.writeUInt16LE(TOMBSTONE_OFFSET, pos);
    return true;
  }

  /**
   * 碎片整理：将存活记录按槽位顺序向页尾对齐压实，
   * 重写全部槽位偏移，空闲空间合并为一段连续区域。
   */
  compact(): void {
    const staging = Buffer.alloc(this.pageSize);
    let newFreeEnd = this.pageSize;
    const newOffsets = new Array<number>(this.slotCount);

    for (let i = 0; i < this.slotCount; i++) {
      if (!this.isLive(i)) continue;
      const pos = this.slotOffsetPos(i);
      const offset = this.buf.readUInt16LE(pos);
      const length = this.buf.readUInt16LE(pos + 2);
      newFreeEnd -= length;
      this.buf.copy(staging, newFreeEnd, offset, offset + length);
      newOffsets[i] = newFreeEnd;
    }

    // 数据区整体替换，空闲区清零。
    staging.copy(this.buf, this.freeStart, this.freeStart);
    this.buf.fill(0, this.freeStart, newFreeEnd);
    for (let i = 0; i < this.slotCount; i++) {
      if (newOffsets[i] !== undefined) {
        this.buf.writeUInt16LE(newOffsets[i], this.slotOffsetPos(i));
      }
    }
    this.freeEnd = newFreeEnd;
    this.fragmentedBytes = 0;
  }

  stats(): PageStats {
    let liveRecords = 0;
    let recordBytes = 0;
    for (let i = 0; i < this.slotCount; i++) {
      if (this.isLive(i)) {
        liveRecords++;
        recordBytes += this.buf.readUInt16LE(this.slotOffsetPos(i) + 2);
      }
    }
    return {
      pageSize: this.pageSize,
      slotCount: this.slotCount,
      liveRecords,
      tombstones: this.slotCount - liveRecords,
      headerBytes: HEADER_SIZE,
      slotArrayBytes: this.slotCount * SLOT_SIZE,
      recordBytes,
      fragmentedBytes: this.fragmentedBytes,
      freeBytes: this.freeSpace(),
      utilization: recordBytes / this.pageSize,
    };
  }
}
