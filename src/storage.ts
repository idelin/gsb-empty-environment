import { promises as fs } from 'node:fs';
import { SparseVector, MemoryAccountant } from './sparse.ts';

interface DiskPage {
  pageLSN: number;
  blob: string;
}

export interface Frame {
  pageLSN: number;
  data: SparseVector;
  dirty: boolean;
}

export class PageStore {
  readonly accountant = new MemoryAccountant();
  private frames = new Map<number, Frame>();
  private disk = new Map<number, DiskPage>();
  readonly path: string;

  private constructor(path: string) {
    this.path = path;
  }

  static async open(path: string): Promise<PageStore> {
    const store = new PageStore(path);
    try {
      const raw = JSON.parse(await fs.readFile(path, 'utf8')) as { pages?: Record<string, DiskPage> };
      for (const [id, page] of Object.entries(raw.pages ?? {})) {
        store.disk.set(Number(id), page);
      }
    } catch (err) {
      if ((err as { code?: string }).code !== 'ENOENT') throw err;
    }
    return store;
  }

  readPage(pageId: number): Frame {
    let frame = this.frames.get(pageId);
    if (!frame) {
      const onDisk = this.disk.get(pageId);
      const data = onDisk
        ? SparseVector.deserialize(Buffer.from(onDisk.blob, 'base64'), { accountant: this.accountant })
        : new SparseVector({ accountant: this.accountant });
      frame = { pageLSN: onDisk?.pageLSN ?? 0, data, dirty: false };
      this.frames.set(pageId, frame);
    }
    return frame;
  }

  setSlot(pageId: number, slot: number, value: number, lsn: number): number {
    const frame = this.readPage(pageId);
    const before = frame.data.get(slot);
    frame.data.set(slot, value);
    frame.pageLSN = lsn;
    frame.dirty = true;
    return before;
  }

  getSlot(pageId: number, slot: number): number {
    return this.readPage(pageId).data.get(slot);
  }

  pageLSN(pageId: number): number {
    return this.readPage(pageId).pageLSN;
  }

  get dirtyCount(): number {
    let n = 0;
    for (const frame of this.frames.values()) if (frame.dirty) n++;
    return n;
  }

  get frameCount(): number {
    return this.frames.size;
  }

  async flushAll(): Promise<void> {
    for (const [pageId, frame] of this.frames) {
      if (!frame.dirty) continue;
      this.disk.set(pageId, {
        pageLSN: frame.pageLSN,
        blob: Buffer.from(frame.data.serialize()).toString('base64'),
      });
      frame.dirty = false;
    }
    await this.persist();
  }

  crash(): void {
    this.evictFrames();
  }

  evictAll(): void {
    this.evictFrames();
  }

  private evictFrames(): void {
    for (const frame of this.frames.values()) frame.data.clear();
    this.frames.clear();
  }

  private async persist(): Promise<void> {
    const tmp = `${this.path}.tmp`;
    const payload = JSON.stringify({ pages: Object.fromEntries(this.disk) });
    await fs.writeFile(tmp, payload, 'utf8');
    await fs.rename(tmp, this.path);
  }
}
