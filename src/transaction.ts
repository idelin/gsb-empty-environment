/**
 * 异步资源事务作用域。
 *
 * 语义：
 * - 资源通过 `scope.acquire()` 按顺序获取；
 * - 任一步失败（获取失败 / 业务回调抛错 / 外部取消）时，
 *   已取得的资源按逆序释放（LIFO 回滚）；
 * - 取消可能发生在“获取已成功、回调尚未处理”的窗口期：
 *   迟到资源由作用域接管并立即释放，不存在责任空隙；
 * - 获取错误（首个失败原因）与全部释放错误都被保留：
 *   无释放错误时原样抛出首个失败原因，
 *   否则抛出 `TransactionError`（cause 为首个失败原因，
 *   releaseErrors 按发生顺序保留全部释放错误）。
 */

export type AcquireFn<T> = () => T | Promise<T>;
export type ReleaseFn<T> = (value: T) => void | Promise<void>;
export type CleanupFn = () => void | Promise<void>;

/** 回滚期间同时发生释放错误时抛出的聚合错误。 */
export class TransactionError extends Error {
  /** 触发回滚的首个失败原因（获取错误 / 业务错误 / 取消原因）。 */
  readonly cause: unknown;
  /** 回滚过程中全部释放错误，按发生顺序排列。 */
  readonly releaseErrors: readonly unknown[];

  constructor(cause: unknown, releaseErrors: readonly unknown[]) {
    super(
      `transaction rolled back with ${releaseErrors.length} release error(s)`,
    );
    this.name = 'TransactionError';
    this.cause = cause;
    this.releaseErrors = releaseErrors.slice();
  }
}

type ScopeState = 'open' | 'closing' | 'closed';

interface Entry {
  release: CleanupFn;
}

export class TransactionScope {
  private entries: Entry[] = [];
  private state: ScopeState = 'open';
  private primaryFailure: unknown;
  private hasPrimaryFailure = false;
  private releaseErrors: unknown[] = [];
  private pendingAcquires = 0;
  private drainWaiters: Array<() => void> = [];
  private closePromise: Promise<void> | null = null;
  private readonly closedSignal: Promise<void>;
  private resolveClosed!: () => void;

  constructor() {
    this.closedSignal = new Promise<void>((resolve) => {
      this.resolveClosed = resolve;
    });
  }

  get isOpen(): boolean {
    return this.state === 'open';
  }

  get isClosed(): boolean {
    return this.state === 'closed';
  }

  /**
   * 获取一个资源并登记其释放回调。
   *
   * 无责任空隙的关键：`await acquire()` 返回后、登记前的窗口期内，
   * 作用域可能已因取消/失败开始回滚。此时资源已到手但无人负责，
   * 由本方法接管并立即释放，然后向调用方抛出作用域的失败原因。
   * 状态检查与登记之间没有任何 await，因此不会出现
   * “既未登记也未释放”的空隙。
   */
  async acquire<T>(acquire: AcquireFn<T>, release: ReleaseFn<T>): Promise<T> {
    if (this.state !== 'open') {
      throw this.currentFailure();
    }
    this.pendingAcquires++;
    try {
      const value = await acquire();
      if (this.state !== 'open') {
        // 迟到资源：作用域已开始回滚，接管并立即释放。
        try {
          await release(value);
        } catch (releaseError) {
          this.releaseErrors.push(releaseError);
        }
        throw this.currentFailure();
      }
      this.entries.push({ release: () => release(value) });
      return value;
    } finally {
      this.pendingAcquires--;
      if (this.pendingAcquires === 0) {
        const waiters = this.drainWaiters.splice(0);
        for (const wake of waiters) wake();
      }
    }
  }

  /** 登记一个无返回值的清理回调，参与逆序回滚。 */
  defer(cleanup: CleanupFn): void {
    if (this.state !== 'open') {
      throw this.currentFailure();
    }
    this.entries.push({ release: cleanup });
  }

  /**
   * 开始回滚（幂等；首个失败原因生效）。
   * 返回的回滚 Promise 永不 reject：所有释放错误都汇入 releaseErrors。
   */
  fail(failure: unknown): Promise<void> {
    if (this.closePromise === null) {
      this.primaryFailure = failure;
      this.hasPrimaryFailure = true;
      this.state = 'closing';
      this.closePromise = this.rollback();
    }
    return this.closePromise;
  }

  /**
   * 提交：封闭作用域，已登记资源的所有权移交调用方，不再回滚。
   * 提交后再调用 acquire/defer 会立即抛错，防止“已获取但无人负责”。
   */
  commit(): void {
    if (this.state === 'open') {
      this.state = 'closed';
      this.entries.splice(0);
      this.resolveClosed();
    }
  }

  /** 回滚完成（作用域关闭）时 resolve；若作用域永不关闭则永不 resolve。 */
  get whenClosed(): Promise<void> {
    return this.closedSignal;
  }

  /**
   * 构造应抛给调用方的错误：
   * 无释放错误时返回首个失败原因本身，否则返回 TransactionError 聚合。
   */
  buildError(): unknown {
    if (this.releaseErrors.length === 0) {
      return this.primaryFailure;
    }
    return new TransactionError(this.primaryFailure, this.releaseErrors);
  }

  private currentFailure(): unknown {
    return this.hasPrimaryFailure
      ? this.primaryFailure
      : new Error('transaction scope is closed');
  }

  private async rollback(): Promise<void> {
    // 先等待在途获取完成所有权交接（登记成功或被接管释放），
    // 再统一逆序释放，保证严格的 LIFO 顺序。
    while (this.pendingAcquires > 0) {
      await new Promise<void>((resolve) => this.drainWaiters.push(resolve));
    }
    const entries = this.entries.splice(0).reverse();
    for (const entry of entries) {
      try {
        await entry.release();
      } catch (releaseError) {
        this.releaseErrors.push(releaseError);
      }
    }
    this.state = 'closed';
    this.resolveClosed();
  }
}

export interface TransactionOptions {
  /** 取消信号：取消即触发回滚，在途获取的迟到资源会被接管释放。 */
  signal?: AbortSignal;
}

/**
 * 在事务作用域内执行 `body`。
 *
 * - `body` 成功返回：事务提交，已获取资源的所有权移交给调用方（不释放）；
 * - `body` 抛错 / 获取失败 / signal 取消：逆序释放全部已取得资源后，
 *   以首个失败原因（或聚合的 TransactionError）reject。
 */
export async function transaction<T>(
  body: (scope: TransactionScope) => T | Promise<T>,
  options: TransactionOptions = {},
): Promise<T> {
  const scope = new TransactionScope();
  const signal = options.signal;

  if (signal !== undefined && signal.aborted) {
    await scope.fail(abortReason(signal));
    throw scope.buildError();
  }

  const onAbort = (): void => {
    void scope.fail(abortReason(signal as AbortSignal));
  };
  signal?.addEventListener('abort', onAbort, { once: true });

  try {
    const bodyPromise = Promise.resolve().then(() => body(scope));
    // 结果由下方统一处理；此处避免取消竞速时的未处理拒绝。
    bodyPromise.catch(() => undefined);

    type Outcome =
      | { type: 'value'; value: T }
      | { type: 'error'; error: unknown }
      | { type: 'closed' };

    const outcome: Outcome = await Promise.race([
      bodyPromise.then<Outcome, Outcome>(
        (value) => ({ type: 'value', value }),
        (error) => ({ type: 'error', error }),
      ),
      scope.whenClosed.then<Outcome>(() => ({ type: 'closed' })),
    ]);

    if (outcome.type === 'closed') {
      // 取消/回滚先于 body 完成（body 可能仍卡在不可取消的等待上）。
      throw scope.buildError();
    }
    if (outcome.type === 'error') {
      await scope.fail(outcome.error);
      throw scope.buildError();
    }
    if (!scope.isOpen) {
      // body 返回期间作用域已被取消：取消语义优先，等待回滚完成。
      await scope.whenClosed;
      throw scope.buildError();
    }
    scope.commit();
    return outcome.value;
  } finally {
    signal?.removeEventListener('abort', onAbort);
  }
}

function abortReason(signal: AbortSignal): unknown {
  // Node < 17.2 的 AbortSignal 没有 reason，回退为 AbortError。
  const reason = (signal as { reason?: unknown }).reason;
  if (reason !== undefined) {
    return reason;
  }
  const error = new Error('The operation was aborted');
  error.name = 'AbortError';
  return error;
}
