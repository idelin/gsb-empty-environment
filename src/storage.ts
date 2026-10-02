/**
 * 存储层：DiskManager（RLE 压缩页镜像）+ BufferPool（脏页表 / steal,no-force）。
 */

import { SparsePage, type RLEBlock } from './sparse.ts';
import type { LSN } from './log.ts';

export interface DiskImage {
  pageLSN: LSN;
  blocks: RLEBlock[];
}

export class DiskManager {
  private readonly pages = new Map<number, DiskImage>();

  read(pageId: number): DiskImage | undefined {
    const image = this.pages.get(pageId);
    return image === undefined
      ? undefined
      : { pageLSN: image.pageLSN, blocks: image.blocks.map((b) => ({ ...b })) };
  }

  write(pageId: number, image: DiskImage): void {
    this.pages.set(pageId, { pageLSN: image.pageLSN, blocks: image.blocks.map((b) => ({ ...b })) });
  }

  pageLSN(pageId: number): LSN {
    return this.pages.get(pageId)?.pageLSN ?? 0;
  }

  snapshot(): Map<number, DiskImage> {
    return new Map([...this.pages.entries()].map(([id, img]) => [id, this.read(id)!]));
  }
}

interface Frame {
  page: SparsePage;
  dirty: boolean;
}

export class BufferPool {
  private readonly frames = new Map<number, Frame>();
  /** 运行时脏页表：pageId -> recLSN（首次变脏的 LSN）。 */
  readonly dirtyPageTable = new Map<number, LSN>();

  private readonly disk: DiskManager;

  constructor(disk: DiskManager) {
    this.disk = disk;
  }

  fetchPage(pageId: number): SparsePage {
    let frame = this.frames.get(pageId);
    if (frame === undefined) {
      const image = this.disk.read(pageId);
      const page = image === undefined
        ? new SparsePage(pageId)
        : SparsePage.fromRLE(pageId, image.blocks, image.pageLSN);
      frame = { page, dirty: false };
      this.frames.set(pageId, frame);
    }
    return frame.page;
  }

  markDirty(pageId: number, lsn: LSN): void {
    const frame = this.frames.get(pageId);
    if (frame === undefined) throw new Error(`page ${pageId} not in buffer pool`);
    frame.dirty = true;
    if (!this.dirtyPageTable.has(pageId)) this.dirtyPageTable.set(pageId, lsn);
  }

  flushPage(pageId: number): void {
    const frame = this.frames.get(pageId);
    if (frame !== undefined && frame.dirty) {
      this.disk.write(pageId, { pageLSN: frame.page.pageLSN, blocks: frame.page.toRLE() });
      frame.dirty = false;
    }
    this.dirtyPageTable.delete(pageId);
  }

  flushAll(): void {
    for (const pageId of [...this.frames.keys()]) this.flushPage(pageId);
  }

  /** 模拟崩溃：丢弃全部内存状态，不落盘。 */
  crash(): void {
    for (const frame of this.frames.values()) frame.page.dispose();
    this.frames.clear();
    this.dirtyPageTable.clear();
  }

  /** 干净关闭：落盘并释放全部页帧，用于泄漏断言。 */
  shutdown(): void {
    this.flushAll();
    for (const frame of this.frames.values()) frame.page.dispose();
    this.frames.clear();
    this.dirtyPageTable.clear();
  }

  get frameCount(): number {
    return this.frames.size;
  }

  get dirtyCount(): number {
    return this.dirtyPageTable.size;
  }
}
