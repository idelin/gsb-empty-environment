import { crc32 } from './crc32.ts';
import { DiskFullError, TornPageError, type FaultInjector } from './faults.ts';
import type { Page, PageId } from './types.ts';

interface StoredPage {
  crc: number;
  payload: string;
}

/**
 * Simulated durable page storage. Pages are CRC-protected; a power cut in the
 * middle of a write leaves a torn page whose checksum will not verify.
 */
export class DiskStorage {
  private readonly pages = new Map<PageId, StoredPage>();
  private readonly injector: FaultInjector;

  constructor(injector: FaultInjector) {
    this.injector = injector;
  }

  writePage(page: Page): void {
    if (this.injector.check('storage.writePage')) {
      throw new DiskFullError(`cannot write page ${page.pageId}: disk full`);
    }
    const payload = JSON.stringify(page);
    if (this.injector.check('storage.tornWrite')) {
      // Power loss mid-write: partial payload with the old (now wrong) CRC.
      this.pages.set(page.pageId, { crc: crc32(payload), payload: payload.slice(0, payload.length >> 1) });
      return;
    }
    this.pages.set(page.pageId, { crc: crc32(payload), payload });
  }

  readPage(pageId: PageId): Page | undefined {
    const stored = this.pages.get(pageId);
    if (!stored) return undefined;
    if (crc32(stored.payload) !== stored.crc) throw new TornPageError(pageId);
    return JSON.parse(stored.payload) as Page;
  }

  pageIds(): PageId[] {
    return [...this.pages.keys()].sort((a, b) => a - b);
  }
}
