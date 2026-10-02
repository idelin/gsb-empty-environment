import { BufferPool } from "./buffer-pool.ts";
import type { LogRecord, Storage } from "./storage.ts";

type EntryState = "buffered" | "persisted" | "dropped";

interface Entry {
  record: LogRecord;
  bufferId: number;
  state: EntryState;
}

export type BarrierState = "pending" | "resolved" | "rejected";

export interface Barrier {
  /** 屏障边界：LSN <= lsn 的日志全部落盘后屏障完成。 */
  readonly lsn: number;
  readonly state: BarrierState;
  readonly settled: Promise<void>;
}

interface BarrierInternal {
  lsn: number;
  state: BarrierState;
  settled: Promise<void>;
  resolve: () => void;
  reject: (err: Error) => void;
}

export interface PipelineStats {
  nextLsn: number;
  committedLsn: number;
  publishedLsn: number;
  buffered: number;
}

/**
 * 高吞吐顺序日志管道：
 * - append() 同步分配单调递增 LSN，多生产者并发安全（单线程事件循环下原子）；
 * - 写盘由单写者链按 LSN 顺序串行执行，保证落盘顺序；
 * - 屏障前的日志全部落盘前，屏障之后的日志禁止发布；
 * - 写盘失败时回滚：丢弃未落盘条目并释放其内存缓冲区，LSN 回退可继续写入。
 */
export class LogStreamPipeline {
  private nextLsn = 1;
  private committedLsn = 0;
  private publishedLsn = 0;
  private readonly entries = new Map<number, Entry>();
  private readonly barriers: BarrierInternal[] = [];
  private writeChain: Promise<void> = Promise.resolve();
  private rolledBackLsn: number | null = null;

  readonly pool = new BufferPool();

  private readonly storage: Storage;

  /** 发布回调：仅当条目已落盘且其之前的所有屏障均已完成时触发。 */
  onPublish: ((record: LogRecord) => void) | null = null;

  constructor(storage: Storage) {
    this.storage = storage;
  }

  /** 分配 LSN 并缓冲日志，返回单调递增的 LSN。 */
  append(streamId: string, payload: string): number {
    const lsn = this.nextLsn++;
    const entry: Entry = {
      record: { lsn, streamId, payload },
      bufferId: this.pool.acquire(),
      state: "buffered",
    };
    this.entries.set(lsn, entry);
    this.enqueuePersist(entry);
    return lsn;
  }

  /** 在指定 LSN 边界插入屏障（可预注册，边界日志可尚未写入）。 */
  insertBarrierAt(lsn: number): Barrier {
    return this.addBarrier(lsn);
  }

  /** 在当前写入尖端插入屏障：此前所有 append 的日志落盘后屏障完成。 */
  insertBarrier(): Barrier {
    return this.addBarrier(this.nextLsn - 1);
  }

  /** 等待写盘链排空（全部落盘或回滚完成）。 */
  whenIdle(): Promise<void> {
    return this.writeChain;
  }

  get stats(): PipelineStats {
    return {
      nextLsn: this.nextLsn,
      committedLsn: this.committedLsn,
      publishedLsn: this.publishedLsn,
      buffered: this.pool.outstanding,
    };
  }

  private addBarrier(lsn: number): Barrier {
    let resolve!: () => void;
    let reject!: (err: Error) => void;
    const settled = new Promise<void>((res, rej) => {
      resolve = res;
      reject = rej;
    });
    // 避免未处理的 rejection 警告；状态仍可通过 state 字段观察。
    settled.catch(() => {});
    const barrier: BarrierInternal = { lsn, state: "pending", settled, resolve, reject };
    this.barriers.push(barrier);
    this.barriers.sort((a, b) => a.lsn - b.lsn);
    this.checkBarriers();
    return barrier;
  }

  private enqueuePersist(entry: Entry): void {
    this.writeChain = this.writeChain.then(() => this.persist(entry));
  }

  private async persist(entry: Entry): Promise<void> {
    if (entry.state !== "buffered") return; // 已被回滚丢弃
    try {
      await this.storage.append(entry.record);
    } catch (err) {
      this.rollback(entry.record.lsn, err);
      return;
    }
    entry.state = "persisted";
    this.pool.release(entry.bufferId);
    if (entry.record.lsn === this.committedLsn + 1) {
      this.committedLsn = entry.record.lsn;
    }
    this.checkBarriers();
    this.drainPublish();
  }

  /** 回滚：丢弃 LSN >= failedLsn 的未落盘条目，释放缓冲区，拒绝越界屏障。 */
  private rollback(failedLsn: number, cause: unknown): void {
    this.rolledBackLsn = failedLsn;
    for (const [lsn, entry] of this.entries) {
      if (lsn >= failedLsn && entry.state === "buffered") {
        entry.state = "dropped";
        this.pool.release(entry.bufferId);
        this.entries.delete(lsn);
      }
    }
    for (const barrier of this.barriers) {
      if (barrier.state === "pending" && barrier.lsn >= failedLsn) {
        barrier.state = "rejected";
        barrier.reject(
          new Error(`barrier@${barrier.lsn} rejected by rollback at LSN ${failedLsn}`),
        );
      }
    }
    // LSN 回退：后续写入复用被丢弃的 LSN 空间。
    this.nextLsn = failedLsn;
  }

  private checkBarriers(): void {
    for (const barrier of this.barriers) {
      if (barrier.state === "pending" && barrier.lsn <= this.committedLsn) {
        barrier.state = "resolved";
        barrier.resolve();
      }
    }
  }

  /** 发布闸门：条目已落盘，且其之前的所有屏障均已完成后才允许发布。 */
  private isPublishable(lsn: number): boolean {
    for (const barrier of this.barriers) {
      if (barrier.lsn < lsn && barrier.state !== "resolved") return false;
    }
    return true;
  }

  private drainPublish(): void {
    for (;;) {
      const next = this.publishedLsn + 1;
      const entry = this.entries.get(next);
      if (!entry || entry.state !== "persisted") return;
      if (!this.isPublishable(next)) return;
      this.entries.delete(next);
      this.publishedLsn = next;
      this.onPublish?.(entry.record);
    }
  }

  /** 仅供测试/演示观察：最近一次回滚发生的 LSN。 */
  get lastRollbackLsn(): number | null {
    return this.rolledBackLsn;
  }
}
