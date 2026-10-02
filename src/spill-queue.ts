import { promises as fs } from 'node:fs';
import type { FileHandle } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { randomBytes } from 'node:crypto';

export interface SpillQueueOptions<T> {
  /** 内存中允许驻留的最大字节数，超出后溢写临时磁盘 */
  maxMemoryBytes: number;
  serialize: (item: T) => Buffer;
  deserialize: (buf: Buffer) => T;
  /** 溢写文件目录，默认为系统临时目录 */
  dir?: string;
}

/**
 * 内存 + 磁盘混合 FIFO 队列。
 *
 * 内存占用低于 maxMemoryBytes 时元素驻留内存；超额后追加写入临时文件
 * （4 字节长度前缀 + 序列化 payload）。只要磁盘上还有未读数据，新元素
 * 继续落盘，保证全局 FIFO 顺序。消费侧优先读内存，内存耗尽后从磁盘读回。
 */
export class SpillQueue<T> {
  private readonly maxMemoryBytes: number;
  private readonly serialize: (item: T) => Buffer;
  private readonly deserialize: (buf: Buffer) => T;
  private readonly dir: string;

  private mem: Array<{ item: T; bytes: number }> = [];
  private memBytes = 0;

  private filePath: string | null = null;
  private fileHandle: FileHandle | null = null;
  private spillOffsets: number[] = [];
  private readIndex = 0;
  private writeOffset = 0;
  private spilledTotal = 0;
  private count = 0;
  private closed = false;

  constructor(options: SpillQueueOptions<T>) {
    this.maxMemoryBytes = options.maxMemoryBytes;
    this.serialize = options.serialize;
    this.deserialize = options.deserialize;
    this.dir = options.dir ?? tmpdir();
  }

  get size(): number {
    return this.count;
  }

  /** 当前内存驻留字节数 */
  get memoryBytes(): number {
    return this.memBytes;
  }

  /** 累计溢写到磁盘的字节数 */
  get spilledBytes(): number {
    return this.spilledTotal;
  }

  get spillFilePath(): string | null {
    return this.filePath;
  }

  async push(item: T, bytes: number): Promise<void> {
    if (this.closed) {
      throw new Error('SpillQueue 已关闭');
    }
    const diskBacklog = this.readIndex < this.spillOffsets.length;
    if (diskBacklog || this.memBytes + bytes > this.maxMemoryBytes) {
      await this.spill(item);
    } else {
      this.mem.push({ item, bytes });
      this.memBytes += bytes;
    }
    this.count++;
  }

  async shift(): Promise<T | undefined> {
    const fromMem = this.mem.shift();
    if (fromMem !== undefined) {
      this.memBytes -= fromMem.bytes;
      this.count--;
      return fromMem.item;
    }
    if (this.readIndex < this.spillOffsets.length) {
      const handle = this.fileHandle;
      if (handle === null) {
        throw new Error('溢写文件未打开');
      }
      const offset = this.spillOffsets[this.readIndex++];
      const header = Buffer.allocUnsafe(4);
      await handle.read(header, 0, 4, offset);
      const length = header.readUInt32LE(0);
      const payload = Buffer.allocUnsafe(length);
      await handle.read(payload, 0, length, offset + 4);
      this.count--;
      return this.deserialize(payload);
    }
    return undefined;
  }

  /** 关闭并删除临时溢写文件 */
  async close(): Promise<void> {
    this.closed = true;
    if (this.fileHandle !== null) {
      await this.fileHandle.close();
      this.fileHandle = null;
    }
    if (this.filePath !== null) {
      await fs.unlink(this.filePath).catch(() => undefined);
      this.filePath = null;
    }
  }

  private async spill(item: T): Promise<void> {
    const handle = await this.ensureFile();
    const payload = this.serialize(item);
    const header = Buffer.allocUnsafe(4);
    header.writeUInt32LE(payload.length, 0);
    const offset = this.writeOffset;
    await handle.writev([header, payload], offset);
    // 必须在写入完成后才登记偏移量：
    // 消费端依据 spillOffsets 判断磁盘可读数据，提前登记会导致读到未落盘的脏数据
    this.spillOffsets.push(offset);
    this.writeOffset += 4 + payload.length;
    this.spilledTotal += 4 + payload.length;
  }

  private async ensureFile(): Promise<FileHandle> {
    if (this.fileHandle === null) {
      this.filePath = join(this.dir, `txn-spill-${process.pid}-${randomBytes(6).toString('hex')}.bin`);
      this.fileHandle = await fs.open(this.filePath, 'w+');
    }
    return this.fileHandle;
  }
}
