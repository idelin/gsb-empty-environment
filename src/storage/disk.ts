import { existsSync, openSync, readSync, writeSync, closeSync } from "node:fs";
import { EngineError, ErrorCodes } from "../errors.ts";
import type { MetricsRegistry } from "../metrics/registry.ts";

export interface DiskPage {
  pageLSN: number;
  data: string;
}

/**
 * Fixed-size page file. Page layout: [8-byte pageLSN][UTF-8 payload].
 * All I/O is synchronous and wrapped in latency histograms.
 */
export class DiskManager {
  private fd: number;
  private closedFlag = false;
  private readonly readTimer;
  private readonly writeTimer;
  readonly path: string;
  readonly pageSize: number;
  readonly numPages: number;
  private readonly metrics: MetricsRegistry;

  constructor(path: string, pageSize: number, numPages: number, metrics: MetricsRegistry) {
    this.path = path;
    this.pageSize = pageSize;
    this.numPages = numPages;
    this.metrics = metrics;
    const fresh = !existsSync(path);
    this.fd = openSync(path, fresh ? "w+" : "r+");
    if (fresh) {
      const zero = new Uint8Array(pageSize * numPages);
      writeSync(this.fd, zero);
    }
    this.readTimer = metrics.timer("disk.read.duration_ms");
    this.writeTimer = metrics.timer("disk.write.duration_ms");
  }

  private offsetOf(pageId: number): number {
    if (!Number.isInteger(pageId) || pageId < 0 || pageId >= this.numPages) {
      throw new EngineError(ErrorCodes.RECOVERY_PAGE_MISSING, `page ${pageId} out of range`, {
        pageId,
        numPages: this.numPages,
      });
    }
    return pageId * this.pageSize;
  }

  readPage(pageId: number): DiskPage {
    const offset = this.offsetOf(pageId);
    const buf = new Uint8Array(this.pageSize);
    this.readTimer.time(() => {
      readSync(this.fd, buf, 0, this.pageSize, offset);
    });
    this.metrics.counter("disk.reads").inc();
    const view = new DataView(buf.buffer);
    const pageLSN = Number(view.getBigUint64(0, true));
    const data = new TextDecoder().decode(buf.subarray(8)).replace(/\0+$/, "");
    return { pageLSN, data };
  }

  writePage(pageId: number, page: DiskPage): void {
    const offset = this.offsetOf(pageId);
    const buf = new Uint8Array(this.pageSize);
    const view = new DataView(buf.buffer);
    view.setBigUint64(0, BigInt(page.pageLSN), true);
    buf.set(new TextEncoder().encode(page.data).subarray(0, this.pageSize - 8), 8);
    this.writeTimer.time(() => {
      writeSync(this.fd, buf, 0, this.pageSize, offset);
    });
    this.metrics.counter("disk.writes").inc();
  }

  close(): void {
    if (!this.closedFlag) {
      this.closedFlag = true;
      closeSync(this.fd);
    }
  }

  get closed(): boolean {
    return this.closedFlag;
  }
}
