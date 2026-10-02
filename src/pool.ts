/**
 * RingBufferPool —— 定长块环形内存池。
 *
 * 一次性预分配 blockSize * blockCount 字节的连续内存（默认 64 块 × 64KB = 4MB），
 * 写入方按环形顺序填充块；读出的数据以 Slice（零拷贝引用）形式交给消费者，
 * 消费者释放引用后对应块才被回收复写。常驻内存恒等于池容量，与流量无关。
 *
 * 约束：单个切片不跨块（帧长必须 <= blockSize），从而回收只需按块计数。
 */
import { Slice, type SliceOwner } from "./slice.ts";

export class PoolExhaustedError extends Error {
  constructor(
    message = "ring buffer pool exhausted: consumer is not releasing slices fast enough",
  ) {
    super(message);
    this.name = "PoolExhaustedError";
  }
}

export interface PoolOptions {
  /** 单块字节数，默认 64 * 1024。 */
  blockSize?: number;
  /** 块数量，默认 64（即默认总容量 4MB）。 */
  blockCount?: number;
}

export interface PoolStats {
  blockSize: number;
  blockCount: number;
  totalBytes: number;
  writableBytes: number;
  outstandingSlices: number;
  liveReferences: number;
  referencedBlocks: number;
  slicesCreated: number;
  idleSlices: number;
}

export class RingBufferPool implements SliceOwner {
  readonly blockSize: number;
  readonly blockCount: number;
  readonly buffer: ArrayBuffer;

  private readonly bytes: Uint8Array;
  private readonly blockRefs: Int32Array;
  private writeIndex = 0;
  private writeOffset = 0;
  private outstandingSlices = 0;
  private liveReferences = 0;
  private referencedBlocks = 0;
  private slicesCreated = 0;
  private readonly idleSlices: Slice[] = [];

  constructor(options: PoolOptions = {}) {
    this.blockSize = options.blockSize ?? 64 * 1024;
    this.blockCount = options.blockCount ?? 64;
    if (this.blockSize <= 0 || this.blockCount < 2) {
      throw new RangeError("blockSize must be > 0 and blockCount must be >= 2");
    }
    this.buffer = new ArrayBuffer(this.blockSize * this.blockCount);
    this.bytes = new Uint8Array(this.buffer);
    this.blockRefs = new Int32Array(this.blockCount);
  }

  /** 从当前写位置起、遇到首个仍被引用的块为止的连续可写字节数。 */
  get writableBytes(): number {
    let free = this.blockSize - this.writeOffset;
    let index = this.writeIndex;
    for (let i = 1; i < this.blockCount; i++) {
      index = (index + 1) % this.blockCount;
      if (this.blockRefs[index] !== 0) break;
      free += this.blockSize;
    }
    return free;
  }

  canWrite(length: number): boolean {
    return length <= this.blockSize && length <= this.writableBytes;
  }

  /**
   * 把一帧数据写入环形缓冲，返回指向池内内存的零拷贝 Slice。
   * 写入本身是一次拷贝（等价于 socket 收包），切片引用不产生额外拷贝。
   * 空间不足时抛出 PoolExhaustedError（背压信号）。
   */
  write(data: Uint8Array): Slice {
    const length = data.length;
    if (length > this.blockSize) {
      throw new RangeError(
        `frame of ${length} bytes exceeds block size ${this.blockSize}`,
      );
    }
    if (length > this.blockSize - this.writeOffset) {
      this.advanceBlock();
    }
    const offset = this.writeIndex * this.blockSize + this.writeOffset;
    this.bytes.set(data, offset);
    this.writeOffset += length;
    return this.acquireSlice(this.writeIndex, offset, length);
  }

  stats(): PoolStats {
    return {
      blockSize: this.blockSize,
      blockCount: this.blockCount,
      totalBytes: this.buffer.byteLength,
      writableBytes: this.writableBytes,
      outstandingSlices: this.outstandingSlices,
      liveReferences: this.liveReferences,
      referencedBlocks: this.referencedBlocks,
      slicesCreated: this.slicesCreated,
      idleSlices: this.idleSlices.length,
    };
  }

  /** SliceOwner 接口：Slice.retain() 时回调。 */
  retainBlock(blockIndex: number): void {
    this.blockRefs[blockIndex]++;
    this.liveReferences++;
  }

  /** SliceOwner 接口：Slice.release() 时回调。 */
  releaseBlock(blockIndex: number): void {
    if (this.blockRefs[blockIndex] === 1) this.referencedBlocks--;
    this.blockRefs[blockIndex]--;
    this.liveReferences--;
  }

  /** SliceOwner 接口：Slice 引用归零时回收对象。 */
  recycleSlice(slice: Slice): void {
    this.outstandingSlices--;
    this.idleSlices.push(slice);
  }

  private advanceBlock(): void {
    const next = (this.writeIndex + 1) % this.blockCount;
    if (this.blockRefs[next] !== 0) {
      throw new PoolExhaustedError();
    }
    this.writeIndex = next;
    this.writeOffset = 0;
  }

  private acquireSlice(blockIndex: number, offset: number, length: number): Slice {
    if (this.blockRefs[blockIndex] === 0) this.referencedBlocks++;
    this.blockRefs[blockIndex]++;
    this.liveReferences++;
    this.outstandingSlices++;
    const slice = this.idleSlices.pop() ?? this.createSlice();
    slice._init(this, this.buffer, blockIndex, offset, length);
    return slice;
  }

  private createSlice(): Slice {
    this.slicesCreated++;
    return new Slice();
  }
}
