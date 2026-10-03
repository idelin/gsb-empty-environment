import { sha256, keyToPageId } from './hash.ts';

export interface PageSnapshot {
  pageId: number;
  hash: string;
  data: Buffer;
  entryCount: number;
}

/**
 * 内存状态树：key 空间被静态切片为 pageCount 个页，
 * 每个页独立序列化并计算哈希。页内数据不可变快照，
 * 写入与快照可并发（单线程事件循环 + 显式让出）。
 */
export class StateTree {
  private pages = new Map<number, Map<string, Buffer>>();
  private lastModified = new Map<number, number>();
  private counter = 0;

  readonly pageCount: number;

  constructor(pageCount: number = 1024) {
    this.pageCount = pageCount;
  }

  get writeCounter(): number {
    return this.counter;
  }

  get size(): number {
    let n = 0;
    for (const page of this.pages.values()) n += page.size;
    return n;
  }

  pageIdOf(key: string): number {
    return keyToPageId(key, this.pageCount);
  }

  set(key: string, value: Buffer | string): void {
    const pid = this.pageIdOf(key);
    let page = this.pages.get(pid);
    if (!page) {
      page = new Map();
      this.pages.set(pid, page);
    }
    page.set(key, typeof value === 'string' ? Buffer.from(value, 'utf8') : value);
    this.lastModified.set(pid, ++this.counter);
  }

  get(key: string): Buffer | undefined {
    return this.pages.get(this.pageIdOf(key))?.get(key);
  }

  /** 自 sinceCounter 之后被修改过的页（增量差量来源） */
  dirtyPages(sinceCounter: number): number[] {
    const dirty: number[] = [];
    for (const [pid, ts] of this.lastModified) {
      if (ts > sinceCounter) dirty.push(pid);
    }
    return dirty.sort((a, b) => a - b);
  }

  /** 全量页列表 */
  allPageIds(): number[] {
    return [...this.pages.keys()].sort((a, b) => a - b);
  }

  /**
   * 对单个页做只读快照：同步序列化，保证该页数据在
   * 序列化瞬间自洽（不受并发写入撕裂）。
   */
  snapshotPage(pageId: number): PageSnapshot | null {
    const page = this.pages.get(pageId);
    if (!page || page.size === 0) return null;
    const keys = [...page.keys()].sort();
    const parts: Buffer[] = [];
    const head = Buffer.alloc(4);
    head.writeUInt32LE(keys.length, 0);
    parts.push(head);
    for (const key of keys) {
      const value = page.get(key)!;
      const keyBuf = Buffer.from(key, 'utf8');
      const meta = Buffer.alloc(8);
      meta.writeUInt32LE(keyBuf.length, 0);
      meta.writeUInt32LE(value.length, 4);
      parts.push(meta, keyBuf, value);
    }
    const data = Buffer.concat(parts);
    return { pageId, hash: sha256(data), data, entryCount: keys.length };
  }
}

/** 反序列化页数据（恢复用） */
export function decodePage(data: Buffer): Map<string, Buffer> {
  const entries = new Map<string, Buffer>();
  let offset = 0;
  const count = data.readUInt32LE(offset);
  offset += 4;
  for (let i = 0; i < count; i++) {
    const keyLen = data.readUInt32LE(offset);
    const valLen = data.readUInt32LE(offset + 4);
    offset += 8;
    const key = data.subarray(offset, offset + keyLen).toString('utf8');
    offset += keyLen;
    const value = Buffer.from(data.subarray(offset, offset + valLen));
    offset += valLen;
    entries.set(key, value);
  }
  return entries;
}
