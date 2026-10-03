import * as fs from 'node:fs';
import * as path from 'node:path';
import type { LSN, PageId, PageImage } from './types.ts';

/** 磁盘页存储：整个库序列化为单个 JSON 文件（模拟稳定存储）。 */
export class DiskStorage {
  private pages = new Map<PageId, PageImage>();
  private readonly filePath: string;
  /** 统计写盘次数，供测试断言 Redo 幂等性 */
  writeCount = 0;

  constructor(dir: string) {
    this.filePath = path.join(dir, 'data.json');
    if (fs.existsSync(this.filePath)) {
      const raw = JSON.parse(fs.readFileSync(this.filePath, 'utf8')) as {
        pages: [PageId, PageImage][];
      };
      this.pages = new Map(raw.pages);
    }
  }

  readPage(pageId: PageId): PageImage {
    const p = this.pages.get(pageId);
    return p ? { ...p } : { data: '', pageLSN: 0 };
  }

  persist(): void {
    const dir = path.dirname(this.filePath);
    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(
      this.filePath,
      JSON.stringify({ pages: [...this.pages.entries()] }, null, 2),
    );
  }

  writePage(pageId: PageId, image: PageImage): void {
    this.pages.set(pageId, { ...image });
    this.writeCount += 1;
    this.persist();
  }

  allPages(): Map<PageId, PageImage> {
    return new Map([...this.pages.entries()].map(([k, v]) => [k, { ...v }]));
  }
}

interface Frame {
  image: PageImage;
  dirty: boolean;
}

/**
 * LRU 缓冲池。容量有限，淘汰脏页时写回磁盘。
 * 调用方须保证 WAL 已刷盘（本实现中 WAL 追加即同步落盘，故恒成立）。
 */
export class BufferPool {
  private frames = new Map<PageId, Frame>();
  private readonly capacity: number;
  private readonly storage: DiskStorage;
  /** 校验 WAL 先写协议的钩子：返回当前已持久化的最大 LSN */
  private readonly flushedLSN: () => LSN;
  evictionCount = 0;

  constructor(storage: DiskStorage, capacity: number, flushedLSN: () => LSN) {
    this.storage = storage;
    this.capacity = capacity;
    this.flushedLSN = flushedLSN;
  }

  fetch(pageId: PageId): PageImage {
    const hit = this.frames.get(pageId);
    if (hit) {
      // LRU：移到最新位置
      this.frames.delete(pageId);
      this.frames.set(pageId, hit);
      return hit.image;
    }
    if (this.frames.size >= this.capacity) {
      this.evictOne();
    }
    const image = this.storage.readPage(pageId);
    this.frames.set(pageId, { image, dirty: false });
    return image;
  }

  /** 修改页内容并标记脏页，pageLSN 为推进该页的日志 LSN */
  set(pageId: PageId, data: string, pageLSN: LSN): void {
    this.fetch(pageId);
    const frame = this.frames.get(pageId)!;
    frame.image.data = data;
    frame.image.pageLSN = pageLSN;
    frame.dirty = true;
  }

  private evictOne(): void {
    const oldest = this.frames.keys().next();
    if (oldest.done) return;
    const pageId = oldest.value;
    const frame = this.frames.get(pageId)!;
    if (frame.dirty) {
      if (frame.image.pageLSN > this.flushedLSN()) {
        throw new Error(`WAL protocol violated: page ${pageId} pageLSN=${frame.image.pageLSN}`);
      }
      this.storage.writePage(pageId, frame.image);
      this.evictionCount += 1;
    }
    this.frames.delete(pageId);
  }

  /** 刷出全部脏页，返回被写盘的页及其 pageLSN（用于清理脏页表） */
  flushAll(): Map<PageId, LSN> {
    const flushed = new Map<PageId, LSN>();
    for (const [pageId, frame] of this.frames) {
      if (frame.dirty) {
        if (frame.image.pageLSN > this.flushedLSN()) {
          throw new Error(`WAL protocol violated: page ${pageId}`);
        }
        this.storage.writePage(pageId, frame.image);
        flushed.set(pageId, frame.image.pageLSN);
        frame.dirty = false;
      }
    }
    return flushed;
  }

  get cachedPageIds(): PageId[] {
    return [...this.frames.keys()];
  }
}
