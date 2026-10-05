/**
 * Structured concurrency for asynchronous tasks.
 *
 * A `TaskScope` lets a parent task spawn child tasks. When the parent (or any
 * descendant) fails, cancellation is *requested* for the whole subtree, but
 * the scope only settles after every descendant has finished its cleanup —
 * including descendants that are spawned while the scope is already closing.
 *
 * All failures (from task bodies and from cleanup handlers) are aggregated
 * into a single `TaskScopeError`, ordered stably by the creation sequence
 * number of the task that produced them. Cancellation errors that are a
 * direct consequence of the shutdown are never reported, and a cancellation
 * error can never become the root cause (`error.cause`).
 */

export class CancelledError extends Error {
  constructor(message = 'Task was cancelled') {
    super(message);
    this.name = 'CancelledError';
  }
}

export type TaskPhase = 'run' | 'cleanup';

export interface TaskFailure {
  /** Creation sequence number of the task that produced the failure. */
  readonly seq: number;
  /** Whether the failure came from the task body or from a cleanup handler. */
  readonly phase: TaskPhase;
  readonly error: unknown;
}

export function isCancellationError(error: unknown): boolean {
  if (error instanceof CancelledError) return true;
  if (typeof error === 'object' && error !== null) {
    return (error as { name?: unknown }).name === 'AbortError';
  }
  return false;
}

export class TaskScopeError extends Error {
  /** All failures of the subtree, sorted stably by creation sequence. */
  readonly failures: readonly TaskFailure[];

  constructor(failures: readonly TaskFailure[]) {
    // The root cause is the first real error in creation order; cancellation
    // errors are only used as a last resort and never override a real cause.
    const root = failures.find((f) => !isCancellationError(f.error)) ?? failures[0];
    super(
      `Task scope failed with ${failures.length} error(s)`,
      root === undefined ? undefined : { cause: root.error },
    );
    this.name = 'TaskScopeError';
    this.failures = failures;
  }

  get rootCause(): unknown {
    return this.cause;
  }
}

export interface TaskHandle<T> {
  /** Creation sequence number of the spawned task. */
  readonly seq: number;
  /** Rejects with a `TaskScopeError` if the child subtree fails. */
  readonly result: Promise<T>;
}

interface ScopeContext {
  nextSeq: number;
}

type ScopeState = 'active' | 'closing' | 'closed';

type CleanupHandler = () => void | Promise<void>;

type TaskBody<T> = (scope: TaskScope) => T | Promise<T>;

export class TaskScope {
  /** Creation sequence number, unique and monotonically increasing per tree. */
  readonly seq: number;

  #ctx: ScopeContext;
  #parent: TaskScope | null;
  #controller = new AbortController();
  #children = new Set<TaskScope>();
  #cleanups: CleanupHandler[] = [];
  #cleanupsExecuted = false;
  #ownFailures: TaskFailure[] = [];
  #childFailures: TaskFailure[] = [];
  #state: ScopeState = 'active';
  #bodySettled = false;
  #closeStarted = false;
  #bodyValue: unknown;
  #detachAbort: (() => void) | null = null;
  #closedPromise: Promise<TaskFailure[]>;
  #resolveClosed!: (failures: TaskFailure[]) => void;

  private constructor(ctx: ScopeContext | null, parent: TaskScope | null) {
    this.#ctx = ctx ?? { nextSeq: 0 };
    this.#parent = parent;
    this.seq = this.#ctx.nextSeq;
    this.#ctx.nextSeq += 1;
    this.#closedPromise = new Promise((resolve) => {
      this.#resolveClosed = resolve;
    });
  }

  /** Runs `body` in a fresh root scope and waits for the whole subtree. */
  static async run<T>(body: TaskBody<T>): Promise<T> {
    const scope = new TaskScope(null, null);
    const failures = await scope.#start(body);
    if (failures.length > 0) throw new TaskScopeError(failures);
    return scope.#bodyValue as T;
  }

  /** Aborted when cancellation is requested for this scope's subtree. */
  get signal(): AbortSignal {
    return this.#controller.signal;
  }

  get isClosing(): boolean {
    return this.#state !== 'active';
  }

  get isClosed(): boolean {
    return this.#state === 'closed';
  }

  /** Resolves once the scope and its whole subtree have fully closed. */
  get completion(): Promise<void> {
    return this.#closedPromise.then(() => undefined);
  }

  /** Throws a `CancelledError` if cancellation has been requested. */
  throwIfCancelled(): void {
    const { signal } = this.#controller;
    if (signal.aborted) {
      throw isCancellationError(signal.reason)
        ? (signal.reason as Error)
        : new CancelledError();
    }
  }

  /**
   * Registers a cleanup handler, run (LIFO) after the body settles and all
   * children have drained. Cleanup errors are aggregated like body errors.
   */
  onCleanup(handler: CleanupHandler): void {
    if (this.#state === 'closed' || this.#cleanupsExecuted) {
      throw new Error('Cannot register a cleanup handler: the scope is already closed');
    }
    this.#cleanups.push(handler);
  }

  /**
   * Spawns a child task. Spawning is still allowed while the scope is
   * closing (e.g. from a cleanup handler); such children start with an
   * already-aborted signal and are awaited before the scope settles.
   */
  spawn<T>(body: TaskBody<T>): TaskHandle<T> {
    if (this.#state === 'closed') {
      throw new Error('Cannot spawn a task: the scope is already closed');
    }
    const child = new TaskScope(this.#ctx, this);
    this.#children.add(child);
    const signal = this.#controller.signal;
    if (signal.aborted) {
      child.#requestClose();
    } else {
      const onAbort = (): void => child.#requestClose();
      signal.addEventListener('abort', onAbort, { once: true });
      child.#detachAbort = () => signal.removeEventListener('abort', onAbort);
    }
    const done = child.#start(body);
    const result: Promise<T> = done.then((failures) => {
      if (failures.length > 0) throw new TaskScopeError(failures);
      return child.#bodyValue as T;
    });
    // Failures are aggregated by the scope itself; mark the handle as
    // handled so an un-awaited handle does not produce unhandled rejections.
    result.catch(() => undefined);
    return { seq: child.seq, result };
  }

  #start(body: TaskBody<unknown>): Promise<TaskFailure[]> {
    void (async (): Promise<void> => {
      try {
        this.#bodyValue = await body(this);
      } catch (error) {
        // A cancellation error thrown while our own cancellation was
        // requested is an expected consequence of the shutdown, not a
        // failure — it must not be recorded nor override the root cause.
        if (!(isCancellationError(error) && this.#controller.signal.aborted)) {
          this.#recordError('run', error);
          this.#parent?.#onChildFailed();
        }
        this.#requestClose();
      } finally {
        this.#bodySettled = true;
        if (this.#state === 'active') this.#state = 'closing';
        this.#maybeClose();
      }
    })();
    return this.#closedPromise;
  }

  #recordError(phase: TaskPhase, error: unknown): void {
    // A re-thrown aggregate (e.g. from awaiting a child handle) is flattened
    // so failures keep their original creation sequence numbers.
    if (error instanceof TaskScopeError) {
      this.#ownFailures.push(...error.failures);
      return;
    }
    this.#ownFailures.push({ seq: this.seq, phase, error });
  }

  /** Requests shutdown: aborts the subtree's signal and starts closing. */
  #requestClose(): void {
    if (this.#state === 'closed') return;
    if (this.#state === 'active') this.#state = 'closing';
    if (!this.#controller.signal.aborted) {
      this.#controller.abort(new CancelledError('Task scope is closing'));
    }
    this.#maybeClose();
  }

  #maybeClose(): void {
    if (this.#state !== 'closing' || !this.#bodySettled || this.#closeStarted) {
      return;
    }
    this.#closeStarted = true;
    void this.#close();
  }

  async #close(): Promise<void> {
    for (;;) {
      // Drain children. Children spawned while we are closing (including
      // from cleanup handlers below) join this set and are awaited too.
      while (this.#children.size > 0) {
        const pending = [...this.#children].map((child) => child.#closedPromise);
        await Promise.allSettled(pending);
      }
      if (this.#cleanupsExecuted) break;
      this.#cleanupsExecuted = true;
      await this.#runCleanups();
      if (this.#children.size === 0) break;
    }
    this.#detachAbort?.();
    this.#detachAbort = null;
    this.#state = 'closed';
    const failures = this.#collectFailures();
    if (this.#parent !== null) {
      this.#parent.#onChildClosed(this, failures);
    }
    this.#resolveClosed(failures);
  }

  async #runCleanups(): Promise<void> {
    const handlers = this.#cleanups;
    this.#cleanups = [];
    for (let i = handlers.length - 1; i >= 0; i -= 1) {
      try {
        await handlers[i]();
      } catch (error) {
        this.#recordError('cleanup', error);
        this.#parent?.#onChildFailed();
      }
    }
  }

  #collectFailures(): TaskFailure[] {
    const all = [...this.#ownFailures, ...this.#childFailures];
    // Array#sort is stable: failures from the same task keep insertion order.
    all.sort((a, b) => a.seq - b.seq);
    return all;
  }

  #onChildClosed(child: TaskScope, failures: TaskFailure[]): void {
    this.#children.delete(child);
    if (failures.length > 0) {
      this.#childFailures.push(...failures);
      this.#requestClose();
    }
  }

  /** Prompt notification so a failure cancels siblings without delay. */
  #onChildFailed(): void {
    this.#requestClose();
  }
}

/** Convenience wrapper around {@link TaskScope.run}. */
export async function runTaskScope<T>(body: TaskBody<T>): Promise<T> {
  return TaskScope.run(body);
}
