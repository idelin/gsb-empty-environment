import type { FileSpec } from './types';

function key(attempt: number, path: string, index: number): string {
  return `${attempt}:${path}:${index}`;
}

/**
 * 产物内容存储（内存实现）。
 * 所有分块按代次隔离，校验失败时按代次整体清理。
 */
export class ContentStore {
  private readonly chunks = new Map<string, Buffer>();

  put(attempt: number, path: string, index: number, data: Buffer): void {
    this.chunks.set(key(attempt, path, index), Buffer.from(data));
  }

  has(attempt: number, path: string, index: number): boolean {
    return this.chunks.has(key(attempt, path, index));
  }

  get(attempt: number, path: string, index: number): Buffer | undefined {
    return this.chunks.get(key(attempt, path, index));
  }

  /** 指定代次当前已收到的分块数。 */
  count(attempt: number): number {
    let n = 0;
    for (const k of this.chunks.keys()) {
      if (k.startsWith(`${attempt}:`)) n += 1;
    }
    return n;
  }

  /** 清理指定代次的全部分块，返回清理数量。 */
  purgeAttempt(attempt: number): number {
    let removed = 0;
    for (const k of [...this.chunks.keys()]) {
      if (k.startsWith(`${attempt}:`)) {
        this.chunks.delete(k);
        removed += 1;
      }
    }
    return removed;
  }

  /** 按清单顺序拼接文件内容。调用前需确认分块齐套。 */
  assemble(attempt: number, file: FileSpec): Buffer {
    const parts: Buffer[] = [];
    for (const c of file.chunks) {
      const data = this.get(attempt, file.path, c.index);
      if (data === undefined) {
        throw new Error(`missing chunk ${file.path}#${c.index} for attempt ${attempt}`);
      }
      parts.push(data);
    }
    return Buffer.concat(parts);
  }
}
