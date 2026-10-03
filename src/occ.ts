/**
 * occ.ts — 无阻塞自旋乐观并发控制（OCC）引擎
 *
 * 经典三阶段执行内核：
 *   1. 读取阶段 (Read)     —— 事务在私有 ReadSet / WriteSet 上工作，写操作全部缓冲在 WriteSet；
 *   2. 验证阶段 (Validate) —— 提交时校验 ReadSet 中每个键的版本是否仍与读取时一致，
 *                             即检测“ReadSet 是否被其他已提交事务修改”；
 *   3. 写入阶段 (Write)    —— 验证通过则把 WriteSet 一次性批量应用到存储，并为每个键赋予新的提交版本。
 *
 * 无阻塞设计：验证 + 写入是一个同步临界区（JS 运行到完成的语义保证其原子性），
 * 不持有任何锁、不等待任何其他事务；冲突时直接中止，由上层自旋重试。
 */

export interface KeyValue {
  value: unknown;
  /** 最后一次修改该键的事务提交版本；键不存在时为 0 */
  version: number;
}

export interface OccStatsSnapshot {
  commits: number;
  aborts: number;
  /** aborts / (commits + aborts)，无事务时为 0 */
  conflictRate: number;
}

export type TxState = 'active' | 'committed' | 'aborted';

export class OccStore {
  private readonly data = new Map<string, KeyValue>();
  /** 全局提交序列号，单调递增；同时作为每个键的版本来源 */
  private commitSeq = 0;
  private commits = 0;
  private aborts = 0;

  /** 开启一个新事务（读取阶段开始） */
  begin(): OccTransaction {
    return new OccTransaction(this);
  }

  /** 读取键当前的值与版本（存储内部视图） */
  peek(key: string): KeyValue {
    const entry = this.data.get(key);
    return entry ? { value: entry.value, version: entry.version } : { value: undefined, version: 0 };
  }

  /** 当前键版本（验证阶段使用） */
  versionOf(key: string): number {
    return this.data.get(key)?.version ?? 0;
  }

  /** 写入阶段：批量应用 WriteSet。同步执行，天然原子。 */
  applyWriteSet(writeSet: ReadonlyMap<string, unknown>): number {
    const commitVersion = ++this.commitSeq;
    for (const [key, value] of writeSet) {
      this.data.set(key, { value, version: commitVersion });
    }
    return commitVersion;
  }

  recordCommit(): void {
    this.commits++;
  }

  recordAbort(): void {
    this.aborts++;
  }

  get stats(): OccStatsSnapshot {
    const total = this.commits + this.aborts;
    return {
      commits: this.commits,
      aborts: this.aborts,
      conflictRate: total === 0 ? 0 : this.aborts / total,
    };
  }

  resetStats(): void {
    this.commits = 0;
    this.aborts = 0;
  }
}

export class OccTransaction {
  /** 读取阶段记录：key -> 读取时观察到的版本 */
  readonly readSet = new Map<string, number>();
  /** 读取阶段缓冲：key -> 待写入的新值 */
  readonly writeSet = new Map<string, unknown>();

  private state: TxState = 'active';

  private readonly store: OccStore;

  constructor(store: OccStore) {
    this.store = store;
  }

  get status(): TxState {
    return this.state;
  }

  /** 读取：写集优先（read-your-own-writes），否则访问存储并记录版本到 ReadSet */
  read<T = unknown>(key: string): T | undefined {
    this.ensureActive();
    if (this.writeSet.has(key)) {
      return this.writeSet.get(key) as T;
    }
    const { value, version } = this.store.peek(key);
    if (!this.readSet.has(key)) {
      this.readSet.set(key, version);
    }
    return value as T | undefined;
  }

  /** 写：仅缓冲到 WriteSet，读取阶段不触碰共享存储 */
  write(key: string, value: unknown): void {
    this.ensureActive();
    this.writeSet.set(key, value);
  }

  /**
   * 提交：验证阶段 + 写入阶段。
   * 验证 ReadSet 中每个键的当前版本是否仍等于读取时版本；
   * 全部一致则一次性批量提交 WriteSet，返回 true；
   * 任一版本过期则干净回滚（WriteSet 从未触碰存储，直接丢弃），返回 false。
   */
  commit(): boolean {
    this.ensureActive();

    // —— 验证阶段 ——
    for (const [key, observedVersion] of this.readSet) {
      if (this.store.versionOf(key) !== observedVersion) {
        this.rollback();
        return false;
      }
    }

    // —— 写入阶段（同步临界区，批量原子提交） ——
    this.store.applyWriteSet(this.writeSet);
    this.store.recordCommit();
    this.state = 'committed';
    return true;
  }

  /** 干净利落的回滚：缓冲的写从未应用，只需丢弃并标记状态 */
  rollback(): void {
    if (this.state !== 'active') return;
    this.writeSet.clear();
    this.readSet.clear();
    this.store.recordAbort();
    this.state = 'aborted';
  }

  private ensureActive(): void {
    if (this.state !== 'active') {
      throw new Error(`transaction is ${this.state}`);
    }
  }
}

export interface RetryOptions {
  /** 最大重试次数（不含首次尝试），默认无限 */
  maxRetries?: number;
  /**
   * 自旋预算：前 spinBudget 次重试用紧循环立即重试（不让出事件循环），
   * 超过后每次重试前让出一次事件循环（setImmediate），避免饿死其他任务。
   */
  spinBudget?: number;
  /** 每次让出事件循环后的退避基数（毫秒），默认 0（仅让出，不睡眠） */
  backoffMs?: number;
}

export class OccRetryExhaustedError extends Error {
  readonly attempts: number;

  constructor(attempts: number) {
    super(`OCC transaction aborted after ${attempts} attempts`);
    this.name = 'OccRetryExhaustedError';
    this.attempts = attempts;
  }
}

const yieldToEventLoop = (): Promise<void> =>
  new Promise((resolve) => setImmediate(resolve));

const sleep = (ms: number): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, ms));

/**
 * 自旋重试执行器：以 OCC 方式运行事务体，冲突中止后自旋重试直到成功。
 * 全程无锁、无阻塞等待其他事务。
 */
export async function runOptimistic<T>(
  store: OccStore,
  body: (tx: OccTransaction) => T | Promise<T>,
  options: RetryOptions = {},
): Promise<T> {
  const { maxRetries = Number.POSITIVE_INFINITY, spinBudget = 8, backoffMs = 0 } = options;

  for (let attempt = 0; ; attempt++) {
    const tx = store.begin();
    const result = await body(tx);
    if (tx.commit()) {
      return result;
    }
    if (attempt >= maxRetries) {
      throw new OccRetryExhaustedError(attempt + 1);
    }
    // 自旋：预算内立即重试；预算外让出事件循环并可加退避
    if (attempt >= spinBudget) {
      await yieldToEventLoop();
      if (backoffMs > 0) await sleep(backoffMs);
    }
  }
}
