import { existsSync, readFileSync, writeFileSync, renameSync } from 'node:fs';
import type { PageId } from '../log/records.ts';

export const PAGE_SIZE = 4096;

/** 页：定长字节区，带 pageLSN（页面上最近一次日志记录的 LSN） */
export interface Page {
  pageId: PageId;
  pageLSN: number;
  data: string[]; // 简化为槽位数组，offset 即槽位下标
}

export function createPage(pageId: PageId, slots = 16): Page {
  return { pageId, pageLSN: 0, data: new Array<string>(slots).fill('') };
}

/**
 * 磁盘存储：单文件 JSON 持久化全部页。
 * 写入采用“临时文件 + 原子 rename”，保证落盘操作本身原子。
 */
export class DiskStorage {
  private readonly filePath: string;

  constructor(filePath: string) {
    this.filePath = filePath;
    if (!existsSync(filePath)) writeFileSync(filePath, '{}');
  }

  readPage(pageId: PageId): Page | null {
    const all = JSON.parse(readFileSync(this.filePath, 'utf8')) as Record<string, Page>;
    const p = all[pageId];
    return p ? { ...p, data: [...p.data] } : null;
  }

  writePage(page: Page): void {
    const all = JSON.parse(readFileSync(this.filePath, 'utf8')) as Record<string, Page>;
    all[page.pageId] = page;
    const tmp = this.filePath + '.tmp';
    writeFileSync(tmp, JSON.stringify(all));
    renameSync(tmp, this.filePath);
  }

  readAll(): Record<string, Page> {
    return JSON.parse(readFileSync(this.filePath, 'utf8')) as Record<string, Page>;
  }
}
