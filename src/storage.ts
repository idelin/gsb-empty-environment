/**
 * Page storage ("disk"). Pages carry a pageLSN: the LSN of the newest log
 * record whose effect is reflected in the page. Redo uses it to decide
 * whether a logged update must be reapplied.
 */

import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname } from 'node:path';
import type { LSN, PageId, Value } from './log-record.ts';

export interface PageState {
  pageLSN: LSN;
  data: Record<string, Value>;
}

export class Storage {
  readonly path: string;
  private pages = new Map<PageId, PageState>();

  private constructor(path: string) {
    this.path = path;
  }

  static async create(path: string, initial: Record<PageId, Record<string, Value>> = {}): Promise<Storage> {
    const storage = new Storage(path);
    for (const [pageId, data] of Object.entries(initial)) {
      storage.pages.set(pageId, { pageLSN: 0, data: { ...data } });
    }
    await storage.flush();
    return storage;
  }

  static async open(path: string): Promise<Storage> {
    const storage = new Storage(path);
    let content: string;
    try {
      content = await readFile(path, 'utf8');
    } catch (err) {
      if ((err as { code?: string }).code === 'ENOENT') return storage;
      throw err;
    }
    const raw = JSON.parse(content) as { pages: Record<PageId, PageState> };
    for (const [pageId, state] of Object.entries(raw.pages)) {
      storage.pages.set(pageId, { pageLSN: state.pageLSN, data: { ...state.data } });
    }
    return storage;
  }

  getPage(pageId: PageId): PageState {
    let page = this.pages.get(pageId);
    if (!page) {
      page = { pageLSN: 0, data: {} };
      this.pages.set(pageId, page);
    }
    return page;
  }

  getValue(pageId: PageId, key: string): Value | undefined {
    const page = this.pages.get(pageId);
    if (!page) return undefined;
    return Object.prototype.hasOwnProperty.call(page.data, key) ? page.data[key] : undefined;
  }

  /** Write a value (null deletes the key) and advance pageLSN. */
  setValue(pageId: PageId, key: string, value: Value, pageLSN: LSN): void {
    const page = this.getPage(pageId);
    if (value === null) {
      delete page.data[key];
    } else {
      page.data[key] = value;
    }
    page.pageLSN = Math.max(page.pageLSN, pageLSN);
  }

  pageIds(): PageId[] {
    return [...this.pages.keys()];
  }

  snapshot(): Record<PageId, PageState> {
    const out: Record<PageId, PageState> = {};
    for (const [id, p] of this.pages) {
      out[id] = { pageLSN: p.pageLSN, data: { ...p.data } };
    }
    return out;
  }

  async flush(): Promise<void> {
    await mkdir(dirname(this.path), { recursive: true });
    const pages: Record<PageId, PageState> = {};
    for (const [id, p] of this.pages) {
      pages[id] = { pageLSN: p.pageLSN, data: p.data };
    }
    await writeFile(this.path, JSON.stringify({ pages }, null, 2), 'utf8');
  }
}
