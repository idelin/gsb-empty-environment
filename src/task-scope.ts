/**
 * Structured concurrency for asynchronous tasks.
 *
 * A TaskScope lets a parent task spawn child tasks. When the parent (or any
 * descendant) fails, cancellation is requested for all descendants, and the
 * scope waits until every task — including cleanup hooks and tasks spawned
 * while the scope is already closing — has fully finished. Failures are
 * aggregated in stable creation order; cancellation errors never mask the
 * original root cause.
 */

export class CancellationError extends Error {
  constructor(message = 'Task was cancelled', options?: { cause?: unknown }) {
    super(message, options);
    this.name = 'CancellationError';
  }
}

/** True for CancellationError and DOMException-style AbortError. */
export function isCancellationError(error: unknown): boolean {
  if (error instanceof CancellationError) return true;
  if (typeof error === 'object' && error !== null) {
    return (error as { name?: unknown }).name === 'AbortError';
  }
  return false;
}

export type TaskErrorPhase = 'body' | 'cleanup';

export interface RecordedTaskError {
  /** Creation sequence number of the task that produced the error (0 = scope body). */
  readonly seq: number;
  /** Whether the error came from the task body or from a cleanup hook. */
  readonly phase: TaskErrorPhase;
  readonly error: unknown;
}

interface TimedTaskError extends RecordedTaskError {
  /** Monotonic recording order, used to pick the chronological root cause. */
  readonly order: number;
}

function describeError(error: unknown): string {
  if (error instanceof Error) return `${error.name}: ${error.message}`;
  return String(error);
}

function compareTimedErrors(a: TimedTaskError, b: TimedTaskError): number {
  if (a.seq !== b.seq) return a.seq - b.seq;
  if (a.phase !== b.phase) return a.phase === 'body' ? -1 : 1;
  return a.order - b.order;
}

/** Aggregated failure of a TaskScope. */
export class TaskScopeError extends Error {
  /** The first non-cancellation error, in chronological order of occurrence. */
  readonly rootCause: unknown;
  /** All non-cancellation errors, sorted by task creation sequence number. */
  readonly errors: readonly RecordedTaskError[];

  constructor(rootCause: unknown, errors: readonly RecordedTaskError[]) {
    super(TaskScopeError.buildMessage(errors), { cause: rootCause });
    this.name = 'TaskScopeError';
    this.rootCause = rootCause;
    this.errors = errors;
  }

  private static buildMessage(errors: readonly RecordedTaskError[]): string {
    const lines = errors.map(
      (entry) => `  - task #${entry.seq} (${entry.phase}): ${describeError(entry.error)}`,
    );
    return [`Task scope failed with ${errors.length} error(s):`, ...lines].join('\n');
  }
}

export type CleanupHook = () => void | Promise<void>;
export type TaskFn<T> = (ctx: TaskContext) => T | Promise<T>;

export interface TaskContext {
  /** Creation sequence number of this task within its scope. */
  readonly seq: number;
  /** Aborted when the scope shuts down or this task is cancelled individually. */
  readonly signal: AbortSignal;
  readonly scope: TaskScope;
  /** Spawn a child task in the same scope. */
  spawn<T>(fn: TaskFn<T>): TaskHandle<T>;
  /**
   * Register a cleanup hook. Hooks run after the task body settles — even on
   * failure or cancellation — in LIFO order. Hooks registered while the scope
   * is closing still run, and the scope waits for them.
   */
  onCleanup(hook: CleanupHook): void;
  /** Throw the cancellation reason if this task has been cancelled. */
  throwIfCancelled(): void;
}

const noop = (): void => {};

export class TaskHandle<T> implements PromiseLike<T> {
  readonly seq: number;
  /** Resolves once the task body and all of its cleanup hooks have finished. Never rejects. */
  readonly settled: Promise<void>;
  readonly #controller: AbortController;
  readonly #result: Promise<T>;

  constructor(
    seq: number,
    controller: AbortController,
    result: Promise<T>,
    settled: Promise<void>,
  ) {
    this.seq = seq;
    this.#controller = controller;
    this.#result = result;
    this.settled = settled;
  }

  get signal(): AbortSignal {
    return this.#controller.signal;
  }

  then<TResult1 = T, TResult2 = never>(
    onfulfilled?: ((value: T) => TResult1 | PromiseLike<TResult1>) | null,
    onrejected?: ((reason: unknown) => TResult2 | PromiseLike<TResult2>) | null,
  ): Promise<TResult1 | TResult2> {
    return this.#result.then(onfulfilled, onrejected);
  }

  catch<TResult = never>(
    onrejected?: ((reason: unknown) => TResult | PromiseLike<TResult>) | null,
  ): Promise<T | TResult> {
    return this.#result.catch(onrejected);
  }

  finally(onfinally?: (() => void) | null): Promise<T> {
    return this.#result.finally(onfinally);
  }

  /** Cancel only this task. Cancellation errors do not fail the scope. */
  cancel(reason?: unknown): void {
    this.#controller.abort(reason ?? new CancellationError());
  }
}

type ScopeState = 'open' | 'closing' | 'closed';

interface TaskEntry {
  readonly seq: number;
  readonly controller: AbortController;
  readonly cleanupHooks: CleanupHook[];
}

function toCancellationError(reason: unknown): Error {
  if (reason instanceof Error) return reason;
  return new CancellationError('Task was cancelled', { cause: reason });
}

export class TaskScope {
  #state: ScopeState = 'open';
  #lastSeq = 0;
  readonly #controller = new AbortController();
  readonly #entries = new Map<number, TaskEntry>();
  readonly #errors: TimedTaskError[] = [];
  #orderCounter = 0;
  #rootCause: unknown;
  #hasRootCause = false;
  #wasCancelled = false;
  #cancelReason: unknown;
  readonly #idleWaiters: Array<() => void> = [];
  #closePromise: Promise<void> | null = null;

  get signal(): AbortSignal {
    return this.#controller.signal;
  }

  get isClosing(): boolean {
    return this.#state !== 'open';
  }

  get isClosed(): boolean {
    return this.#state === 'closed';
  }

  /** Number of tasks still running (body or cleanup). */
  get taskCount(): number {
    return this.#entries.size;
  }

  /**
   * Spawn a child task. Spawning is still allowed while the scope is closing
   * (e.g. from a cleanup hook); such tasks start with an already-aborted
   * signal and are awaited like any other. Spawning after the scope has fully
   * closed throws.
   */
  spawn<T>(fn: TaskFn<T>): TaskHandle<T> {
    if (this.#state === 'closed') {
      throw new Error('Cannot spawn a task: the TaskScope is already closed');
    }
    const seq = ++this.#lastSeq;
    const controller = new AbortController();
    const entry: TaskEntry = { seq, controller, cleanupHooks: [] };
    this.#entries.set(seq, entry);

    const scopeSignal = this.#controller.signal;
    const forwardAbort = (): void => {
      controller.abort(scopeSignal.reason);
    };
    if (scopeSignal.aborted) forwardAbort();
    else scopeSignal.addEventListener('abort', forwardAbort, { once: true });

    const ctx: TaskContext = {
      seq,
      signal: controller.signal,
      scope: this,
      spawn: (childFn) => this.spawn(childFn),
      onCleanup: (hook) => {
        entry.cleanupHooks.push(hook);
      },
      throwIfCancelled: () => {
        if (controller.signal.aborted) throw toCancellationError(controller.signal.reason);
      },
    };

    const bodyPromise = this.#runTask(entry, ctx, fn);
    const settled = bodyPromise.then(noop, noop).finally(() => {
      scopeSignal.removeEventListener('abort', forwardAbort);
      this.#entries.delete(seq);
      this.#releaseIdleWaitersIfEmpty();
    });

    return new TaskHandle(seq, controller, bodyPromise, settled);
  }

  async #runTask<T>(entry: TaskEntry, ctx: TaskContext, fn: TaskFn<T>): Promise<T> {
    let value: T | undefined;
    let failure: unknown;
    let failed = false;
    try {
      value = await fn(ctx);
    } catch (error) {
      failed = true;
      failure = error;
      this.#recordError(entry.seq, 'body', error);
    }
    // Cleanup hooks always run — even after failure or cancellation — LIFO.
    let hook: CleanupHook | undefined;
    while ((hook = entry.cleanupHooks.pop()) !== undefined) {
      try {
        await hook();
      } catch (error) {
        this.#recordError(entry.seq, 'cleanup', error);
        if (!failed) {
          failed = true;
          failure = error;
        }
      }
    }
    if (failed) throw failure;
    return value as T;
  }

  #recordError(seq: number, phase: TaskErrorPhase, error: unknown): void {
    // Cancellation is an expected consequence of shutdown, never a root cause.
    if (isCancellationError(error)) return;
    this.#errors.push({ seq, phase, error, order: this.#orderCounter++ });
    if (!this.#hasRootCause) {
      this.#hasRootCause = true;
      this.#rootCause = error;
    }
    this.#beginShutdown(
      new CancellationError('Task scope is shutting down after a task failure', {
        cause: error,
      }),
    );
  }

  #beginShutdown(reason?: unknown): void {
    if (this.#state === 'open') this.#state = 'closing';
    if (!this.#controller.signal.aborted) {
      this.#controller.abort(reason ?? new CancellationError('Task scope is shutting down'));
    }
  }

  /** Request cancellation of the whole scope; close() will still wait for cleanup. */
  cancel(reason?: unknown): void {
    this.#wasCancelled = true;
    this.#cancelReason = reason ?? new CancellationError('Task scope was cancelled');
    this.#beginShutdown(this.#cancelReason);
  }

  /**
   * Shut the scope down and wait until every task — including tasks spawned
   * while closing — has finished its body and cleanup hooks. Rejects with
   * TaskScopeError if any task failed with a non-cancellation error.
   */
  close(): Promise<void> {
    if (this.#closePromise === null) this.#closePromise = this.#closeInternal();
    return this.#closePromise;
  }

  async #closeInternal(): Promise<void> {
    this.#beginShutdown();
    // Tasks may spawn further tasks while closing, so re-check until drained.
    while (this.#entries.size > 0) {
      await new Promise<void>((resolve) => {
        this.#idleWaiters.push(resolve);
      });
    }
    this.#state = 'closed';
    if (this.#errors.length > 0) {
      const sorted = [...this.#errors].sort(compareTimedErrors);
      const errors: RecordedTaskError[] = sorted.map(({ seq, phase, error }) => ({
        seq,
        phase,
        error,
      }));
      throw new TaskScopeError(this.#rootCause, errors);
    }
    if (this.#wasCancelled) throw this.#cancelReason;
  }

  #releaseIdleWaitersIfEmpty(): void {
    if (this.#entries.size > 0) return;
    const waiters = this.#idleWaiters.splice(0, this.#idleWaiters.length);
    for (const resolve of waiters) resolve();
  }

  /**
   * Run `body` as the parent task of a fresh scope. When the body returns or
   * throws, the scope is closed: descendants are cancelled, all cleanup is
   * awaited, and any failures are aggregated into a TaskScopeError.
   */
  static async run<T>(body: (scope: TaskScope) => T | Promise<T>): Promise<T> {
    const scope = new TaskScope();
    let value: T | undefined;
    let bodyFailure: unknown;
    let bodyFailed = false;
    try {
      value = await body(scope);
    } catch (error) {
      bodyFailed = true;
      bodyFailure = error;
      scope.#recordError(0, 'body', error);
    }
    // close() surfaces a TaskScopeError that already contains the body error.
    await scope.close();
    if (bodyFailed) throw bodyFailure;
    return value as T;
  }
}

/** Convenience alias for TaskScope.run. */
export function runTaskScope<T>(body: (scope: TaskScope) => T | Promise<T>): Promise<T> {
  return TaskScope.run(body);
}
