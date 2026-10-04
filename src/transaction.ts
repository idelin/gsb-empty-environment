/**
 * 异步资源事务作用域。
 *
 * 语义：
 * - 资源按调用顺序获取（use），失败时按逆序释放（rollback）。
 * - 取消（AbortSignal）可能发生在「获取已成功、但回调尚未处理」的竞态窗口。
 *   此时作用域已关闭，迟到落地的资源所有权仍归作用域，必须就地接管释放，
 *   不允许出现「既未提交、也未释放」的责任空隙。
 * - 获取错误与释放错误都被保留：获取/业务错误作为 TransactionError.cause，
 *   释放错误逐个收集进 TransactionError.releaseErrors。
 *
 * 不变量：runTransaction 结算（settle）时，任何已完成的获取要么已提交移交
 * 给调用方（commit），要么已被释放（rollback / 迟到接管）。
 */

export interface AcquireContext {
  /** 作用域内部信号：回滚或提交开始时中止，供获取方自行取消。 */
  readonly signal: AbortSignal;
}

export interface ResourceSpec<T> {
  readonly name?: string;
  acquire(ctx: AcquireContext): T | Promise<T>;
  release(value: T): void | Promise<void>;
}

export interface ReleaseFailure {
  /** 获取顺序号（从 0 开始），迟到资源也占用序号。 */
  readonly index: number;
  readonly name: string | undefined;
  readonly error: unknown;
}

export interface CommittedResource<T = unknown> {
  readonly index: number;
  readonly name: string | undefined;
  readonly value: T;
  release(): Promise<void>;
}

export class ScopeClosedError extends Error {
  constructor(message = 'transaction scope is closed') {
    super(message);
    this.name = 'ScopeClosedError';
  }
}

export class TransactionError extends Error {
  /** 触发回滚的原始错误（获取失败、业务异常或中止原因）。 */
  readonly acquireError: unknown;
  /** 回滚/接管过程中发生的全部释放错误，按发生顺序。 */
  readonly releaseErrors: readonly ReleaseFailure[];

  constructor(acquireError: unknown, releaseErrors: readonly ReleaseFailure[]) {
    super(buildMessage(acquireError, releaseErrors));
    this.name = 'TransactionError';
    this.acquireError = acquireError;
    this.releaseErrors = releaseErrors;
    if (acquireError !== undefined) {
      (this as { cause?: unknown }).cause = acquireError;
    }
  }
}

type ScopeState = 'open' | 'rolling-back' | 'rolled-back' | 'committed';

interface Entry {
  readonly index: number;
  readonly name: string | undefined;
  readonly value: unknown;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  release(value: any): void | Promise<void>;
}

const noop = (): void => {};

function describeError(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function buildMessage(cause: unknown, failures: readonly ReleaseFailure[]): string {
  const base = `transaction rolled back: ${describeError(cause)}`;
  if (failures.length === 0) return base;
  const details = failures
    .map((f) => `#${f.index}${f.name !== undefined ? `(${f.name})` : ''}: ${describeError(f.error)}`)
    .join('; ');
  return `${base}; ${failures.length} release error(s): ${details}`;
}

export class TransactionScope {
  private state: ScopeState = 'open';
  private readonly entries: Entry[] = [];
  private readonly failures: ReleaseFailure[] = [];
  private readonly controller = new AbortController();
  private sequence = 0;
  private pendingCount = 0;
  private drainWaiters: Array<() => void> = [];
  private rollbackPromise: Promise<void> | null = null;
  private committedResources: readonly CommittedResource[] | null = null;

  get isOpen(): boolean {
    return this.state === 'open';
  }

  /** 作用域关闭（回滚或提交开始）时中止。 */
  get signal(): AbortSignal {
    return this.controller.signal;
  }

  /** 迄今为止记录到的全部释放错误（含迟到资源的接管释放）。 */
  get releaseErrors(): readonly ReleaseFailure[] {
    return this.failures;
  }

  /** commit 成功后移交的资源句柄；未提交时为 null。 */
  get committed(): readonly CommittedResource[] | null {
    return this.committedResources;
  }

  /**
   * 获取一个资源并纳入事务管理。
   * 作用域已关闭时同步抛出 ScopeClosedError（快速失败）。
   * 若获取完成时作用域已关闭（迟到资源），资源被就地释放，
   * 返回的 Promise 以 ScopeClosedError 拒绝。
   */
  use<T>(spec: ResourceSpec<T>): Promise<T> {
    if (this.state !== 'open') {
      throw new ScopeClosedError(
        `cannot acquire ${spec.name ?? 'resource'}: scope is ${this.state}`,
      );
    }
    const index = this.sequence++;
    this.pendingCount++;
    const promise = this.doUse(spec, index);
    // 跟踪在途获取：回滚/提交必须等待其落地并完成接管，才能消除责任空隙。
    promise.then(noop, noop).then(() => {
      this.pendingCount--;
      if (this.pendingCount === 0) {
        const waiters = this.drainWaiters;
        this.drainWaiters = [];
        for (const resolve of waiters) resolve();
      }
    });
    // 调用方可能不 await（竞态下 body 已被中止），预先挂接避免未处理拒绝。
    promise.catch(noop);
    return promise;
  }

  private async doUse<T>(spec: ResourceSpec<T>, index: number): Promise<T> {
    const value = await spec.acquire({ signal: this.controller.signal });
    if (this.state !== 'open') {
      // 迟到资源：获取在作用域关闭后才完成。所有权仍在作用域，
      // 必须就地接管释放，否则该资源既未提交也未释放，形成责任空隙。
      await this.releaseOne({ index, name: spec.name, value, release: spec.release });
      throw new ScopeClosedError(
        `acquired ${spec.name ?? 'resource'} after scope closed; resource was released`,
      );
    }
    this.entries.push({ index, name: spec.name, value, release: spec.release });
    return value;
  }

  /**
   * 提交：获取到的资源所有权移交给调用方。
   * 等待在途获取落地（迟到资源被接管释放，不进入移交列表）。
   */
  async commit(): Promise<readonly CommittedResource[]> {
    if (this.state !== 'open') {
      throw new ScopeClosedError(`cannot commit: scope is ${this.state}`);
    }
    this.state = 'committed';
    this.controller.abort();
    await this.waitForDrain();
    const handles: CommittedResource[] = this.entries.map((entry) => ({
      index: entry.index,
      name: entry.name,
      value: entry.value,
      release: () => Promise.resolve(entry.release(entry.value)).then(noop),
    }));
    this.entries.length = 0;
    this.committedResources = handles;
    return handles;
  }

  /**
   * 回滚：逆序释放已获取资源，并等待在途获取落地后接管释放。
   * 幂等；释放错误被收集进 releaseErrors，不会抛出。
   */
  rollback(): Promise<void> {
    if (this.rollbackPromise !== null) return this.rollbackPromise;
    if (this.state === 'committed') {
      return Promise.reject(new ScopeClosedError('cannot rollback a committed scope'));
    }
    this.rollbackPromise = this.doRollback();
    return this.rollbackPromise;
  }

  private async doRollback(): Promise<void> {
    this.state = 'rolling-back';
    this.controller.abort();
    const drained = this.waitForDrain();
    // 已入栈资源立即逆序释放，不被仍在飞行中的获取阻塞。
    while (this.entries.length > 0) {
      await this.releaseOne(this.entries.pop() as Entry);
    }
    // 等待迟到资源完成接管释放：这是消除责任空隙的关键屏障。
    await drained;
    this.state = 'rolled-back';
  }

  private waitForDrain(): Promise<void> {
    if (this.pendingCount === 0) return Promise.resolve();
    return new Promise<void>((resolve) => {
      this.drainWaiters.push(resolve);
    });
  }

  private async releaseOne(entry: Entry): Promise<void> {
    try {
      await entry.release(entry.value);
    } catch (error) {
      this.failures.push({ index: entry.index, name: entry.name, error });
    }
  }
}

export interface RunTransactionOptions {
  /** 外部取消信号。中止即触发回滚。 */
  readonly signal?: AbortSignal;
  /** 提交成功后的资源移交回调。 */
  onCommitted?(resources: readonly CommittedResource[]): void;
}

/** 内部分类标记：中止竞速获胜，区别于 body 自身的拒绝。 */
class AbortRejection {
  readonly reason: unknown;
  constructor(reason: unknown) {
    this.reason = reason;
  }
}

/**
 * 运行一个事务：body 内按序获取资源；
 * body 抛错或 signal 中止 → 逆序回滚并以 TransactionError 拒绝
 * （cause 保留原始错误，releaseErrors 保留全部释放错误）；
 * body 成功 → 提交，资源所有权随 onCommitted 移交。
 *
 * 保证：本函数结算时，不存在「已获取但无人负责」的资源。
 */
export async function runTransaction<R>(
  body: (scope: TransactionScope) => R | Promise<R>,
  options: RunTransactionOptions = {},
): Promise<R> {
  const scope = new TransactionScope();
  const signal = options.signal;

  const bodyPromise = Promise.resolve().then(() => body(scope));
  // 中止竞速获胜后 body 可能稍后才拒绝，预先挂接避免未处理拒绝。
  bodyPromise.catch(noop);

  let aborted = false;
  let rejectAbort: ((error: AbortRejection) => void) | null = null;
  const abortPromise = new Promise<never>((_, reject) => {
    rejectAbort = reject;
  });
  const onAbort = (): void => {
    aborted = true;
    (rejectAbort as (error: AbortRejection) => void)(new AbortRejection(signal?.reason));
  };
  if (signal !== undefined) {
    if (signal.aborted) onAbort();
    else signal.addEventListener('abort', onAbort, { once: true });
  }

  try {
    const result =
      signal === undefined ? await bodyPromise : await Promise.race([bodyPromise, abortPromise]);
    const resources = await scope.commit();
    options.onCommitted?.(resources);
    return result;
  } catch (error) {
    let cause: unknown;
    if (error instanceof AbortRejection) {
      cause = error.reason;
    } else if (aborted && error instanceof ScopeClosedError) {
      // body 的在途 use 因回滚而失败，真正的起因是中止。
      cause = signal?.reason;
    } else {
      cause = error;
    }
    // 回滚会等待在途获取落地并接管释放，之后才能抛出，保证无责任空隙。
    await scope.rollback();
    throw new TransactionError(cause, scope.releaseErrors);
  } finally {
    if (signal !== undefined) signal.removeEventListener('abort', onAbort);
  }
}
