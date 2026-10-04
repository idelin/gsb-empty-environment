import { canonicalize } from './canonicalize';
import { defaultImmutability, ImmutabilityStrategy } from './immutable';
import { Ledger } from './ledger';
import { VersionTracker } from './version-tracker';

export class AbortError extends Error {
  constructor(message = 'the operation was aborted') {
    super(message);
    this.name = 'AbortError';
  }
}

/** 内部哨兵：任务完成时输入版本已推进，结果不得发布，等待者需按当前版本重试。 */
const STALE: unique symbol = Symbol('stale-result');

export interface ComputeFn<I, P, R> {
  (input: I, params: P, signal: AbortSignal): R | Promise<R>;
}

export interface ResultCacheOptions<I, P, R> {
  /** 缓存额度：最多同时保留多少个已发布结果（超出按 LRU 驱逐）。 */
  capacity: number;
  compute: ComputeFn<I, P, R>;
  tracker?: VersionTracker;
  /** 参数 -> 键片段，默认 canonicalize（键序无关、类型敏感）。 */
  paramKey?: (params: P) => string;
  immutability?: ImmutabilityStrategy<R>;
  ledger?: Ledger;
}

interface Entry<R> {
  readonly snapshot: R;
}

interface Inflight {
  readonly key: string;
  readonly underlying: object;
  readonly version: number;
  readonly controller: AbortController;
  subscribers: number;
  cancelled: boolean;
  settled: boolean;
  promise: Promise<unknown>;
}

/**
 * 不可变结果缓存。
 *
 * 键 = 底层身份 #id + 视图形状 [shape] + 版本 @vN + 规范化参数。
 *
 * 不变式：
 * 1. 同键并发请求合并为一次计算，各调用方拿到独立发放值；
 * 2. 参数或输入版本不同 -> 键不同 -> 绝不误命中；
 * 3. 任务完成时若输入版本已推进（旧任务晚到）或已被取消，
 *    结果一律丢弃，绝不发布半成品/过期品；
 * 4. 已发布结果不可变，调用方改返回值不污染缓存；
 * 5. 已发布结果占用额度，超额按 LRU 驱逐并记账。
 */
export class ResultCache<I, P, R> {
  readonly ledger: Ledger;

  private readonly capacity: number;
  private readonly compute: ComputeFn<I, P, R>;
  private readonly tracker: VersionTracker;
  private readonly paramKeyOf: (params: P) => string;
  private readonly immutability: ImmutabilityStrategy<R>;

  private readonly entries = new Map<string, Entry<R>>();
  private readonly inflight = new Map<string, Inflight>();

  constructor(options: ResultCacheOptions<I, P, R>) {
    if (!Number.isInteger(options.capacity) || options.capacity < 1) {
      throw new RangeError('capacity must be a positive integer');
    }
    this.capacity = options.capacity;
    this.compute = options.compute;
    this.tracker = options.tracker ?? new VersionTracker();
    this.paramKeyOf = options.paramKey ?? canonicalize;
    this.immutability = options.immutability ?? defaultImmutability<R>();
    this.ledger = options.ledger ?? new Ledger();
  }

  /** 当前已发布结果数（占用额度）。 */
  get size(): number {
    return this.entries.size;
  }

  run(input: I, params: P, options?: { signal?: AbortSignal }): Promise<R> {
    const signal = options?.signal;
    if (signal?.aborted) {
      return Promise.reject(new AbortError());
    }

    const identity = this.tracker.identityOf(input);
    const shape = VersionTracker.shapeOf(input);
    const version = this.tracker.versionOf(input);
    const paramKey = this.paramKeyOf(params);
    const key = `#${identity}[${shape}]@v${version}|${paramKey}`;

    if (this.entries.has(key)) {
      const entry = this.entries.get(key) as Entry<R>;
      // LRU：命中即刷新为最近使用。
      this.entries.delete(key);
      this.entries.set(key, entry);
      this.ledger.record('hit', key);
      return Promise.resolve(this.immutability.release(entry.snapshot));
    }

    const existing = this.inflight.get(key);
    if (existing !== undefined) {
      this.ledger.record('coalesced', key, `subscribers=${existing.subscribers + 1}`);
      return this.subscribe(existing, input, params, signal);
    }

    this.ledger.record('miss', key);
    const task: Inflight = {
      key,
      underlying: VersionTracker.underlyingOf(input),
      version,
      controller: new AbortController(),
      subscribers: 0,
      cancelled: false,
      settled: false,
      promise: Promise.resolve(),
    };
    this.ledger.record('start', key);

    let started: Promise<R>;
    try {
      started = Promise.resolve(this.compute(input, params, task.controller.signal));
    } catch (err) {
      started = Promise.reject(err);
    }
    task.promise = started.then(
      (raw) => this.finishSuccess(task, raw),
      (err: unknown) => this.finishFailure(task, err),
    );
    this.inflight.set(key, task);
    return this.subscribe(task, input, params, signal);
  }

  private subscribe(
    task: Inflight,
    input: I,
    params: P,
    signal: AbortSignal | undefined,
  ): Promise<R> {
    task.subscribers += 1;
    return new Promise<R>((resolve, reject) => {
      const onAbort = (): void => {
        task.subscribers -= 1;
        this.ledger.record('abort', task.key, `remaining=${task.subscribers}`);
        if (task.subscribers === 0 && !task.settled) {
          // 最后一个等待者离开：取消任务，其后的产出不得发布。
          task.cancelled = true;
          task.controller.abort();
        }
        reject(new AbortError());
      };
      signal?.addEventListener('abort', onAbort, { once: true });
      task.promise.then(
        (snapshot) => {
          signal?.removeEventListener('abort', onAbort);
          resolve(this.immutability.release(snapshot as R));
        },
        (err: unknown) => {
          signal?.removeEventListener('abort', onAbort);
          if (err === STALE) {
            // 输入在计算期间被覆盖：按当前版本重新执行
            // （通常会合并到已在进行的新版本任务上）。
            resolve(this.run(input, params, { signal }));
          } else {
            reject(err);
          }
        },
      );
    });
  }

  private finishSuccess(task: Inflight, raw: R): R {
    task.settled = true;
    this.inflight.delete(task.key);

    if (task.cancelled) {
      this.ledger.record('discard-cancelled', task.key, 'cancelled task must not publish');
      throw new AbortError();
    }
    const currentVersion = this.tracker.versionOf(task.underlying);
    if (currentVersion !== task.version) {
      this.ledger.record('discard-stale', task.key, `input now at v${currentVersion}`);
      throw STALE;
    }

    const snapshot = this.immutability.snapshot(raw);
    this.entries.set(task.key, { snapshot });
    this.ledger.record('store', task.key);
    this.evictIfNeeded();
    return snapshot;
  }

  private finishFailure(task: Inflight, err: unknown): never {
    task.settled = true;
    this.inflight.delete(task.key);
    if (task.cancelled) {
      this.ledger.record('discard-cancelled', task.key, 'compute rejected after abort');
      throw new AbortError();
    }
    this.ledger.record('error', task.key, err instanceof Error ? err.message : String(err));
    throw err;
  }

  private evictIfNeeded(): void {
    while (this.entries.size > this.capacity) {
      const oldest = this.entries.keys().next();
      if (oldest.done === true) return;
      this.entries.delete(oldest.value);
      this.ledger.record('evict', oldest.value, `capacity=${this.capacity}`);
    }
  }
}
