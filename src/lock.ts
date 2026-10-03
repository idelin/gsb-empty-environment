/**
 * lock.ts — 锁方案基线（用于与 OCC 对比）
 *
 * 粗粒度两阶段锁：事务开始时获取全局互斥锁，直到提交/回滚才释放。
 * 所有事务（包括只读事务）严格串行，是教科书式的悲观并发控制对照组。
 */

export interface LockStatsSnapshot {
  commits: number;
  /** 因等待锁而排队的事务数 */
  waits: number;
  /** 累计等锁时间（毫秒） */
  waitTimeMs: number;
}

/** 公平 FIFO 异步互斥锁 */
class AsyncMutex {
  private tail: Promise<void> = Promise.resolve();
  private releaseCurrent: (() => void) | null = null;

  async acquire(): Promise<() => void> {
    const previous = this.tail;
    let release!: () => void;
    this.tail = new Promise<void>((resolve) => {
      release = resolve;
    });
    await previous;
    this.releaseCurrent = release;
    return () => {
      this.releaseCurrent = null;
      release();
    };
  }
}

export class LockStore {
  private readonly data = new Map<string, unknown>();
  private readonly mutex = new AsyncMutex();
  private commits = 0;
  private waits = 0;
  private waitTimeMs = 0;
  /** 等待中 + 持有锁的事务数 */
  private pending = 0;

  read<T = unknown>(key: string): T | undefined {
    return this.data.get(key) as T | undefined;
  }

  write(key: string, value: unknown): void {
    this.data.set(key, value);
  }

  /** 在全局互斥锁保护下串行执行事务体 */
  async run<T>(body: (store: LockStore) => T | Promise<T>): Promise<T> {
    this.pending++;
    const queued = this.pending > 1;
    const start = performance.now();
    const release = await this.mutex.acquire();
    this.pending--;
    if (queued) {
      this.waits++;
      this.waitTimeMs += performance.now() - start;
    }
    try {
      const result = await body(this);
      this.commits++;
      return result;
    } finally {
      release();
    }
  }

  get stats(): LockStatsSnapshot {
    return {
      commits: this.commits,
      waits: this.waits,
      waitTimeMs: this.waitTimeMs,
    };
  }

  resetStats(): void {
    this.commits = 0;
    this.waits = 0;
    this.waitTimeMs = 0;
  }
}
