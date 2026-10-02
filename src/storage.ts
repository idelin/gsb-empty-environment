import fs from 'node:fs';
import path from 'node:path';
import type { JsonValue, LSN, PageId } from './types.ts';

/** 页在磁盘上的镜像：数据 + 该页最近一次应用的日志 LSN。 */
export interface PageImage {
  pageLSN: LSN;
  data: Record<string, JsonValue>;
}

/**
 * 磁盘存储。为简洁起见，整库序列化为单个 JSON 文件（原子替换写入），
 * 接口语义等同于按页存取的磁盘管理器。
 */
export class Storage {
  private pages = new Map<PageId, PageImage>();
  readonly file: string;

  constructor(file: string) {
    this.file = file;
    fs.mkdirSync(path.dirname(file), { recursive: true });
    if (fs.existsSync(file)) {
      const raw = JSON.parse(fs.readFileSync(file, 'utf8')) as Record<string, PageImage>;
      for (const [pageId, image] of Object.entries(raw)) this.pages.set(pageId, image);
    }
  }

  readPage(pageId: PageId): PageImage | undefined {
    const image = this.pages.get(pageId);
    return image ? { pageLSN: image.pageLSN, data: { ...image.data } } : undefined;
  }

  writePage(pageId: PageId, image: PageImage): void {
    this.pages.set(pageId, { pageLSN: image.pageLSN, data: { ...image.data } });
    this.persist();
  }

  private persist(): void {
    const obj: Record<string, PageImage> = {};
    for (const [pageId, image] of this.pages) obj[pageId] = image;
    const tmp = this.file + '.tmp';
    fs.writeFileSync(tmp, JSON.stringify(obj));
    fs.renameSync(tmp, this.file);
  }
}
