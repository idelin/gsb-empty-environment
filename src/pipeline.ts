import type { PersistedRecord, Sink } from './sink.ts';

export interface PipelineStats {
  allocated: number;
  released: number;
  persisted: number;
  rolledBack: number;
}

interface Barrier {
  lsn: number;
  resolve: () => void;
  reject: (err: Error) => void;
}

export class LogStreamPipeline {
  private nextLsn = 1;
  private writtenLsn = 0;
  private durableLsn = 0;
  private publishedLsn = 0;
  private pending: PersistedRecord[] = [];
  private barriers: Barrier[] = [];
  private flushing = false;
  private failure: Error | null = null;
  private readonly idleWaiters: Array<() => void> = [];
  private readonly publishListeners: Array<(lsn: number) => void> = [];
  private readonly sink: Sink;

  readonly stats: PipelineStats = {
    allocated: 0,
    released: 0,
    persisted: 0,
    rolledBack: 0,
  };

  constructor(sink: Sink) {
    this.sink = sink;
  }

  get lastAssignedLsn(): number {
    return this.nextLsn - 1;
  }

  get durable(): number {
    return this.durableLsn;
  }

  get published(): number {
    return this.publishedLsn;
  }

  get bufferedCount(): number {
    return this.pending.length;
  }

  get failed(): boolean {
    return this.failure !== null;
  }

  get failureReason(): Error | null {
    return this.failure;
  }

  append(streamId: string, payload: Buffer | string): number {
    if (this.failure !== null) {
      throw this.failure;
    }
    const lsn = this.nextLsn++;
    const buf = typeof payload === 'string' ? Buffer.from(payload, 'utf8') : payload;
    this.pending.push({ lsn, streamId, payload: buf });
    this.stats.allocated += 1;
    this.kick();
    return lsn;
  }

  insertBarrier(): Promise<void> {
    const lsn = this.nextLsn - 1;
    if (this.failure !== null) {
      return Promise.reject(this.failure);
    }
    if (lsn <= this.durableLsn) {
      return Promise.resolve();
    }
    return new Promise<void>((resolve, reject) => {
      this.barriers.push({ lsn, resolve, reject });
      this.kick();
    });
  }

  onPublish(listener: (lsn: number) => void): void {
    this.publishListeners.push(listener);
  }

  settled(): Promise<void> {
    if (!this.flushing && this.pending.length === 0) {
      return Promise.resolve();
    }
    return new Promise<void>((resolve) => {
      this.idleWaiters.push(resolve);
    });
  }

  async close(): Promise<void> {
    await this.settled();
    await this.sink.close();
  }

  private kick(): void {
    if (this.flushing) {
      return;
    }
    this.flushing = true;
    void this.flushLoop();
  }

  private async flushLoop(): Promise<void> {
    try {
      while (this.pending.length > 0) {
        const batch = this.pending.splice(0, this.pending.length);
        let writtenInBatch = 0;
        try {
          for (const record of batch) {
            await this.sink.write(record);
            this.writtenLsn = record.lsn;
            writtenInBatch += 1;
          }
          await this.sink.sync();
        } catch (err) {
          await this.handleBatchFailure(err as Error, batch, writtenInBatch);
          return;
        }
        this.stats.persisted += batch.length;
        this.stats.released += batch.length;
        this.durableLsn = this.writtenLsn;
        this.advanceBarriers();
        this.advancePublished();
      }
    } finally {
      this.flushing = false;
      if (this.pending.length > 0 && this.failure === null) {
        this.kick();
      } else {
        const waiters = this.idleWaiters.splice(0);
        for (const resolve of waiters) {
          resolve();
        }
      }
    }
  }

  private async handleBatchFailure(
    err: Error,
    batch: PersistedRecord[],
    writtenInBatch: number,
  ): Promise<void> {
    const writtenUnsynced = batch.slice(0, writtenInBatch);
    const unwritten = batch.slice(writtenInBatch);
    let salvaged = false;
    if (writtenUnsynced.length > 0) {
      try {
        await this.sink.sync();
        salvaged = true;
      } catch {
        salvaged = false;
      }
    }
    if (salvaged) {
      this.stats.persisted += writtenUnsynced.length;
      this.stats.released += writtenUnsynced.length;
      this.durableLsn = this.writtenLsn;
      this.advanceBarriers();
      this.advancePublished();
      this.rollback(err, unwritten);
    } else {
      this.rollback(err, [...writtenUnsynced, ...unwritten]);
    }
  }

  private rollback(err: Error, dropped: PersistedRecord[]): void {
    this.failure = err;
    const stillQueued = this.pending.splice(0);
    const total = dropped.length + stillQueued.length;
    this.stats.rolledBack += total;
    this.stats.released += total;
    const barriers = this.barriers.splice(0);
    for (const barrier of barriers) {
      if (barrier.lsn <= this.durableLsn) {
        barrier.resolve();
      } else {
        barrier.reject(err);
      }
    }
  }

  private advanceBarriers(): void {
    while (this.barriers.length > 0 && this.barriers[0]!.lsn <= this.durableLsn) {
      this.barriers.shift()!.resolve();
    }
  }

  private advancePublished(): void {
    if (this.publishedLsn === this.durableLsn) {
      return;
    }
    this.publishedLsn = this.durableLsn;
    for (const listener of this.publishListeners) {
      listener(this.publishedLsn);
    }
  }
}
