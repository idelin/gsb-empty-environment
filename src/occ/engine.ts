import { OccConflictError } from './errors.ts';
import { SpinLatch } from './spin-latch.ts';
import { VersionedStore } from './store.ts';
import { OccTransaction } from './transaction.ts';
import { sleep } from '../util/sleep.ts';

export interface OccEngineOptions {
  /** 冲突自动重试的最大次数（run 使用），默认 16。 */
  maxRetries?: number;
  /** 指数退避的基准毫秒数，默认 1。 */
  baseBackoffMs?: number;
}

export interface RunOptions {
  maxRetries?: number;
}

export interface OccStatsSnapshot<K> {
  readonly attempts: number;
  readonly commits: number;
  readonly aborts: number;
  /** 冲突率 = 中止次数 / 尝试次数。 */
  readonly conflictRate: number;
  /** 按键统计的冲突次数，用于定位热点键。 */
  readonly conflictsByKey: ReadonlyMap<K, number>;
}

/**
 * 无阻塞自旋乐观并发控制（OCC）引擎。
 *
 * 经典三阶段执行内核：
 * 1. 读取阶段：事务在私有 ReadSet/WriteSet 上工作，不加锁、不阻塞；
 * 2. 验证阶段：检查 ReadSet 中每个键的版本是否仍与读取时一致，
 *    即未被其他已提交事务修改；
 * 3. 写入阶段：验证通过则把整个 WriteSet 以同一提交版本一次性批量提交，
 *    否则干净利落回滚（WriteSet 直接丢弃，存储零副作用）。
 */
export class OccEngine<K, V> {
  readonly store: VersionedStore<K, V>;
  private readonly latch = new SpinLatch();
  private readonly maxRetries: number;
  private readonly baseBackoffMs: number;
  private attempts = 0;
  private commits = 0;
  private aborts = 0;
  private readonly conflictsByKey = new Map<K, number>();

  constructor(options: OccEngineOptions = {}) {
    this.store = new VersionedStore<K, V>();
    this.maxRetries = options.maxRetries ?? 16;
    this.baseBackoffMs = options.baseBackoffMs ?? 1;
  }

  /** 开启一个事务（进入读取阶段）。 */
  begin(): OccTransaction<K, V> {
    return new OccTransaction<K, V>(this.store);
  }

  /**
   * 验证阶段 + 写入阶段：同步临界区，一次性批量提交。
   * 验证失败时抛 OccConflictError，事务的所有缓冲写入被丢弃。
   */
  commit(txn: OccTransaction<K, V>): void {
    this.attempts += 1;
    this.latch.acquire();
    try {
      const conflicts = txn.validate();
      if (conflicts.length > 0) {
        this.aborts += 1;
        for (const key of conflicts) {
          this.conflictsByKey.set(key, (this.conflictsByKey.get(key) ?? 0) + 1);
        }
        txn.markAborted();
        throw new OccConflictError(conflicts);
      }
      if (txn.writeSize > 0) {
        const version = this.store.nextCommitVersion();
        this.store.applyWrites(txn.writeSetEntries(), version);
      }
      txn.markCommitted();
      this.commits += 1;
    } finally {
      this.latch.release();
    }
  }

  /** 执行一次完整的三阶段（读取 -> 验证 -> 写入），冲突时不重试。 */
  async tryRun<T>(fn: (txn: OccTransaction<K, V>) => T | Promise<T>): Promise<T> {
    const txn = this.begin();
    const result = await fn(txn);
    this.commit(txn);
    return result;
  }

  /** 带指数退避 + 随机抖动的自动重试版本。 */
  async run<T>(
    fn: (txn: OccTransaction<K, V>) => T | Promise<T>,
    options: RunOptions = {},
  ): Promise<T> {
    const maxRetries = options.maxRetries ?? this.maxRetries;
    let lastError: unknown;
    for (let attempt = 0; attempt <= maxRetries; attempt += 1) {
      try {
        return await this.tryRun(fn);
      } catch (err) {
        if (!(err instanceof OccConflictError)) {
          throw err;
        }
        lastError = err;
        const backoff = this.baseBackoffMs * 2 ** Math.min(attempt, 6) * (0.5 + Math.random());
        await sleep(backoff);
      }
    }
    throw lastError;
  }

  getStats(): OccStatsSnapshot<K> {
    return {
      attempts: this.attempts,
      commits: this.commits,
      aborts: this.aborts,
      conflictRate: this.attempts === 0 ? 0 : this.aborts / this.attempts,
      conflictsByKey: new Map(this.conflictsByKey),
    };
  }

  resetStats(): void {
    this.attempts = 0;
    this.commits = 0;
    this.aborts = 0;
    this.conflictsByKey.clear();
  }
}
