import type { LSN, PageId } from './types.ts';

export interface PageImage {
  value: number;
  pageLSN: LSN;
}

export class Disk {
  private pages = new Map<PageId, PageImage>();

  readPage(pageId: PageId): PageImage {
    const p = this.pages.get(pageId);
    return p ? { ...p } : { value: 0, pageLSN: 0 };
  }

  writePage(pageId: PageId, image: PageImage): void {
    this.pages.set(pageId, { ...image });
  }

  snapshot(): Map<PageId, PageImage> {
    return new Map([...this.pages].map(([k, v]) => [k, { ...v }]));
  }
}

export interface Frame extends PageImage {
  dirty: boolean;
  recLSN: LSN;
}

export class BufferPool {
  private disk: Disk;
  private frames = new Map<PageId, Frame>();

  constructor(disk: Disk) {
    this.disk = disk;
  }

  fix(pageId: PageId): Frame {
    let f = this.frames.get(pageId);
    if (!f) {
      const img = this.disk.readPage(pageId);
      f = { ...img, dirty: false, recLSN: 0 };
      this.frames.set(pageId, f);
    }
    return f;
  }

  markDirty(frame: Frame, lsn: LSN): void {
    if (!frame.dirty) {
      frame.dirty = true;
      frame.recLSN = lsn;
    }
  }

  dirtyPages(): [PageId, LSN][] {
    const out: [PageId, LSN][] = [];
    for (const [id, f] of this.frames) if (f.dirty) out.push([id, f.recLSN]);
    return out;
  }

  flushAll(): void {
    for (const [id, f] of this.frames) {
      if (f.dirty) {
        this.disk.writePage(id, { value: f.value, pageLSN: f.pageLSN });
        f.dirty = false;
      }
    }
  }

  crash(): void {
    this.frames.clear();
  }

  get dirtyCount(): number {
    let n = 0;
    for (const f of this.frames.values()) if (f.dirty) n++;
    return n;
  }
}
