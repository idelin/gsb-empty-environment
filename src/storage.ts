/**
 * Page-oriented disk storage. Each page carries a pageLSN recording the LSN
 * of the most recent log record applied to it — the key to ARIES redo
 * idempotence ("repeating history" without double-applying).
 */

export interface Page {
  pageId: string;
  pageLSN: number;
  data: string;
}

export class PageStore {
  private readonly filePath: string;
  private pages = new Map<string, Page>();

  constructor(filePath: string) {
    this.filePath = filePath;
  }

  async load(): Promise<void> {
    const fs = await import('node:fs/promises');
    let content: string;
    try {
      content = await fs.readFile(this.filePath, 'utf8');
    } catch {
      this.pages = new Map();
      return;
    }
    this.pages = new Map(
      content
        .split('\n')
        .filter((l) => l.trim().length > 0)
        .map((l) => {
          const p = JSON.parse(l) as Page;
          return [p.pageId, p];
        }),
    );
  }

  async flush(): Promise<void> {
    const fs = await import('node:fs/promises');
    const lines = [...this.pages.values()].map((p) => JSON.stringify(p)).join('\n');
    await fs.writeFile(this.filePath, lines ? lines + '\n' : '', 'utf8');
  }

  get(pageId: string): Page | undefined {
    const p = this.pages.get(pageId);
    return p ? { ...p } : undefined;
  }

  /** Apply data + pageLSN atomically (used by normal updates, redo and undo). */
  apply(pageId: string, data: string, pageLSN: number): void {
    this.pages.set(pageId, { pageId, data, pageLSN });
  }

  all(): Page[] {
    return [...this.pages.values()].map((p) => ({ ...p }));
  }
}
