/**
 * 页存储与缓冲池。
 *
 * DiskStorage：唯一持久层，原子写（临时文件 + rename），模拟磁盘。
 * BufferPool：易失页缓存，跟踪每页 pageLSN 与脏页 recLSN；
 * crash() 直接丢弃全部缓存帧，模拟掉电。
 */
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import type { DirtyPageTable, LSN, PageId } from './log-record.ts';

export interface PageImage {
  value: number;
  pageLSN: LSN;
}

export class DiskStorage {
  readonly path: string;
  #pages = new Map<PageId, PageImage>();

  constructor(path: string) {
    this.path = path;
    if (existsSync(path)) {
      const raw = JSON.parse(readFileSync(path, 'utf8')) as Record<string, PageImage>;
      for (const [pageId, image] of Object.entries(raw)) {
        this.#pages.set(pageId, { ...image });
      }
    }
  }

  readPage(pageId: PageId): PageImage {
    const page = this.#pages.get(pageId);
    return page ? { ...page } : { value: 0, pageLSN: 0 };
  }

  /** 写页并原子落盘（写时即持久，等价于缓冲池被强制逐出）。 */
  writePage(pageId: PageId, image: PageImage): void {
    this.#pages.set(pageId, { ...image });
    this.#persist();
  }

  pageIds(): PageId[] {
    return [...this.#pages.keys()].sort();
  }

  snapshot(): Record<PageId, PageImage> {
    const out: Record<PageId, PageImage> = {};
    for (const [pageId, image] of this.#pages) {
      out[pageId] = { ...image };
    }
    return out;
  }

  #persist(): void {
    mkdirSync(dirname(this.path), { recursive: true });
    const tmp = `${this.path}.tmp`;
    writeFileSync(tmp, JSON.stringify(this.snapshot()));
    renameSync(tmp, this.path);
  }
}

interface Frame extends PageImage {
  dirty: boolean;
  /** 本页自上次落盘以来第一条修改它的日志 LSN（DPT 的 recLSN）。 */
  recLSN: LSN;
}

export class BufferPool {
  readonly storage: DiskStorage;
  #frames = new Map<PageId, Frame>();

  constructor(storage: DiskStorage) {
    this.storage = storage;
  }

  get size(): number {
    return this.#frames.size;
  }

  read(pageId: PageId): PageImage {
    const frame = this.#fix(pageId);
    return { value: frame.value, pageLSN: frame.pageLSN };
  }

  /** PageSink 接口：与 DiskStorage 同构，供 Undo 补偿逻辑复用。 */
  readPage(pageId: PageId): PageImage {
    return this.read(pageId);
  }

  /** PageSink 接口：按给定 LSN 应用页映像（用于 CLR 补偿）。 */
  writePage(pageId: PageId, image: PageImage): void {
    this.apply(pageId, image.value, image.pageLSN);
  }

  /** 应用日志记录对应的页修改，并提升 pageLSN（WAL 先行的页侧体现）。 */
  apply(pageId: PageId, value: number, lsn: LSN): void {
    const frame = this.#fix(pageId);
    frame.value = value;
    frame.pageLSN = lsn;
    if (!frame.dirty) {
      frame.dirty = true;
      frame.recLSN = lsn;
    }
  }

  flush(pageId: PageId): void {
    const frame = this.#frames.get(pageId);
    if (frame?.dirty) {
      this.storage.writePage(pageId, { value: frame.value, pageLSN: frame.pageLSN });
      frame.dirty = false;
    }
  }

  flushAll(): void {
    for (const pageId of this.#frames.keys()) {
      this.flush(pageId);
    }
  }

  /** 当前脏页表快照：pageId -> recLSN。 */
  dirtyPageTable(): DirtyPageTable {
    const dpt: DirtyPageTable = new Map();
    for (const [pageId, frame] of this.#frames) {
      if (frame.dirty) {
        dpt.set(pageId, frame.recLSN);
      }
    }
    return dpt;
  }

  hasDirtyPages(): boolean {
    for (const frame of this.#frames.values()) {
      if (frame.dirty) return true;
    }
    return false;
  }

  /** 掉电：丢弃全部易失帧。 */
  crash(): void {
    this.#frames.clear();
  }

  #fix(pageId: PageId): Frame {
    let frame = this.#frames.get(pageId);
    if (!frame) {
      const image = this.storage.readPage(pageId);
      frame = { ...image, dirty: false, recLSN: 0 };
      this.#frames.set(pageId, frame);
    }
    return frame;
  }
}
