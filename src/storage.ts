import { existsSync, readFileSync, writeFileSync, renameSync, mkdirSync } from 'node:fs';
import { dirname } from 'node:path';

export interface PageImage {
  value: number;
  pageLSN: number;
}

/** Page-level durable storage backed by a single JSON data file. */
export class DiskStorage {
  private readonly filePath: string;

  constructor(filePath: string) {
    this.filePath = filePath;
  }

  load(): Map<number, PageImage> {
    if (!existsSync(this.filePath)) return new Map();
    const raw = JSON.parse(readFileSync(this.filePath, 'utf8')) as Record<string, PageImage>;
    return new Map(Object.entries(raw).map(([k, v]) => [Number(k), v]));
  }

  save(pages: Map<number, PageImage>): void {
    mkdirSync(dirname(this.filePath), { recursive: true });
    const tmp = this.filePath + '.tmp';
    writeFileSync(tmp, JSON.stringify(Object.fromEntries(pages)), 'utf8');
    renameSync(tmp, this.filePath);
  }
}

export type CowHook = (pageId: number, oldImage: PageImage) => void;

/**
 * Buffer pool with steal/no-force semantics: dirty pages may be flushed at any
 * time and committed pages are not forced at commit. Supports copy-on-write
 * hooks so a concurrent snapshot can capture pre-write images with low pause.
 */
export class BufferPool {
  private pages = new Map<number, PageImage>();
  private dirty = new Set<number>();
  private cowHooks = new Set<CowHook>();
  private readonly storage: DiskStorage;

  constructor(storage: DiskStorage) {
    this.storage = storage;
    this.pages = storage.load();
  }

  get dirtyCount(): number {
    return this.dirty.size;
  }

  get pageCount(): number {
    return this.pages.size;
  }

  readPage(pageId: number): PageImage {
    return this.pages.get(pageId) ?? { value: 0, pageLSN: 0 };
  }

  /** Apply an in-memory update; page becomes dirty. Fires COW hooks first. */
  writePage(pageId: number, value: number, lsn: number): void {
    const old = this.readPage(pageId);
    for (const hook of this.cowHooks) hook(pageId, { ...old });
    this.pages.set(pageId, { value, pageLSN: lsn });
    this.dirty.add(pageId);
  }

  /** Redo/undo application: identical to writePage but named for call-site clarity. */
  applyLoggedWrite(pageId: number, value: number, lsn: number): void {
    this.writePage(pageId, value, lsn);
  }

  flushPage(pageId: number): void {
    this.storage.save(this.pages);
    this.dirty.delete(pageId);
  }

  flushAll(): void {
    if (this.dirty.size === 0) return;
    this.storage.save(this.pages);
    this.dirty.clear();
  }

  dirtyPages(): number[] {
    return [...this.dirty];
  }

  allPages(): ReadonlyMap<number, PageImage> {
    return this.pages;
  }

  addCowHook(hook: CowHook): void {
    this.cowHooks.add(hook);
  }

  removeCowHook(hook: CowHook): void {
    this.cowHooks.delete(hook);
  }

  get cowHookCount(): number {
    return this.cowHooks.size;
  }
}
