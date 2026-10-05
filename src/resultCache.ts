import { canonicalize } from "./canonicalize";
import { Ledger } from "./ledger";
import { BufferIdentity, VersionRegistry } from "./versionRegistry";

export interface ComputeFn<P, R> {
  (input: ArrayBufferView, params: P, signal: AbortSignal): Promise<R> | R;
}

export interface ResultCacheOptions<P, R> {
  readonly compute: ComputeFn<P, R>;
  /** Maximum published entries kept; least-recently-used entries are evicted. */
  readonly maxEntries: number;
  readonly registry?: VersionRegistry;
  readonly ledger?: Ledger;
  /** Deep-copy strategy for results; defaults to copying arrays/views. */
  readonly copyResult?: (value: R) => R;
}

export interface RequestOptions {
  readonly signal?: AbortSignal;
}

interface CacheEntry<R> {
  readonly value: R;
  lastUsed: number;
}

interface InFlight<R> {
  readonly key: string;
  readonly controller: AbortController;
  promise: Promise<R>;
  subscribers: number;
  cancelled: boolean;
}

function copyView(view: ArrayBufferView): ArrayBufferView {
  if (view instanceof DataView) {
    const bytes = new Uint8Array(view.byteLength);
    bytes.set(new Uint8Array(view.buffer, view.byteOffset, view.byteLength));
    return new DataView(bytes.buffer);
  }
  return (view as unknown as { slice(): ArrayBufferView }).slice();
}

function defaultCopy<R>(value: R): R {
  if (ArrayBuffer.isView(value)) return copyView(value) as R;
  if (Array.isArray(value)) return (value as unknown[]).slice() as unknown as R;
  return value;
}

function abortError(): Error {
  const error = new Error("request cancelled");
  error.name = "AbortError";
  return error;
}

/**
 * Immutable result cache for expensive computations over versioned arrays.
 *
 * - Keys combine the *underlying buffer* version (views share it) with a
 *   canonical serialization of the params, so param changes never false-hit.
 * - Published values are private copies; every caller receives a fresh copy,
 *   so mutating a returned array cannot corrupt the cache or other callers.
 * - Concurrent requests for the same key merge into one computation.
 * - A computation is aborted once its last subscriber cancels, and a result
 *   that arrives after cancellation is dropped instead of published.
 * - A result whose input version was superseded while computing is dropped
 *   as stale instead of being published.
 */
export class ResultCache<P, R> {
  private readonly compute: ComputeFn<P, R>;
  private readonly maxEntries: number;
  private readonly registry: VersionRegistry;
  private readonly ledger: Ledger;
  private readonly copyResult: (value: R) => R;

  private readonly entries = new Map<string, CacheEntry<R>>();
  private readonly inFlight = new Map<string, InFlight<R>>();
  private clock = 0;

  constructor(options: ResultCacheOptions<P, R>) {
    if (options.maxEntries < 1) {
      throw new RangeError("maxEntries must be >= 1");
    }
    this.compute = options.compute;
    this.maxEntries = options.maxEntries;
    this.registry = options.registry ?? new VersionRegistry();
    this.ledger = options.ledger ?? new Ledger();
    this.copyResult = options.copyResult ?? defaultCopy;
  }

  get size(): number {
    return this.entries.size;
  }

  get pendingCount(): number {
    return this.inFlight.size;
  }

  keyFor(input: ArrayBufferView, params: P): string {
    const identity = this.registry.identityOf(input);
    return ResultCache.buildKey(identity, params);
  }

  private static buildKey<P>(identity: BufferIdentity, params: P): string {
    return (
      `buf#${identity.bufferId}@v${identity.version}` +
      `+${identity.byteOffset}:${identity.byteLength}` +
      `|${canonicalize(params)}`
    );
  }

  request(input: ArrayBufferView, params: P, options: RequestOptions = {}): Promise<R> {
    if (options.signal?.aborted) {
      return Promise.reject(abortError());
    }
    const identity = this.registry.identityOf(input);
    const key = ResultCache.buildKey(identity, params);

    const cached = this.entries.get(key);
    if (cached !== undefined) {
      cached.lastUsed = ++this.clock;
      this.ledger.record("hit", key);
      return Promise.resolve(this.copyResult(cached.value));
    }

    const existing = this.inFlight.get(key);
    if (existing !== undefined) {
      this.ledger.record("join", key, `subscribers=${existing.subscribers + 1}`);
      return this.subscribe(existing, options.signal);
    }

    this.ledger.record("miss", key);
    this.ledger.record("compute-start", key);

    const controller = new AbortController();
    const inputSnapshot = copyView(input);
    const flight: InFlight<R> = {
      key,
      controller,
      promise: Promise.resolve(undefined as never),
      subscribers: 0,
      cancelled: false,
    };
    this.inFlight.set(key, flight);

    flight.promise = Promise.resolve()
      .then(() => this.compute(inputSnapshot, params, controller.signal))
      .then(
        (value) => this.settleSuccess(flight, input, identity, value),
        (error: unknown) => this.settleFailure(flight, error),
      );
    // Subscribers attach their own handlers; swallow otherwise-unhandled
    // rejections on the shared completion promise.
    flight.promise.catch(() => undefined);

    return this.subscribe(flight, options.signal);
  }

  private subscribe(flight: InFlight<R>, signal?: AbortSignal): Promise<R> {
    if (signal?.aborted) {
      return Promise.reject(abortError());
    }
    flight.subscribers += 1;
    return new Promise<R>((resolve, reject) => {
      let settled = false;
      const cleanup = () => {
        signal?.removeEventListener("abort", onAbort);
      };
      const onAbort = () => {
        if (settled) return;
        settled = true;
        flight.subscribers -= 1;
        cleanup();
        reject(abortError());
        if (flight.subscribers === 0) {
          this.abortFlight(flight);
        }
      };
      signal?.addEventListener("abort", onAbort, { once: true });
      flight.promise.then(
        (stored) => {
          if (settled) return;
          settled = true;
          cleanup();
          resolve(this.copyResult(stored));
        },
        (error: unknown) => {
          if (settled) return;
          settled = true;
          cleanup();
          reject(error);
        },
      );
    });
  }

  private abortFlight(flight: InFlight<R>): void {
    if (flight.cancelled) return;
    flight.cancelled = true;
    this.inFlight.delete(flight.key);
    this.ledger.record("abort", flight.key, "all subscribers cancelled");
    flight.controller.abort();
  }

  private settleSuccess(
    flight: InFlight<R>,
    input: ArrayBufferView,
    identity: BufferIdentity,
    value: R,
  ): R {
    if (this.inFlight.get(flight.key) === flight) {
      this.inFlight.delete(flight.key);
    }
    if (flight.cancelled) {
      this.ledger.record("cancel-drop", flight.key, "result arrived after abort");
      return value;
    }
    const currentVersion = this.registry.versionOf(input);
    if (currentVersion !== identity.version) {
      this.ledger.record(
        "stale-discard",
        flight.key,
        `computed for v${identity.version}, buffer now at v${currentVersion}`,
      );
      return value;
    }
    this.publish(flight.key, value);
    return value;
  }

  private settleFailure(flight: InFlight<R>, error: unknown): never {
    if (this.inFlight.get(flight.key) === flight) {
      this.inFlight.delete(flight.key);
    }
    if (flight.cancelled) {
      this.ledger.record("cancel-drop", flight.key, "failure arrived after abort");
    } else {
      this.ledger.record("error", flight.key, String(error));
    }
    throw error;
  }

  private publish(key: string, value: R): void {
    this.entries.set(key, { value: this.copyResult(value), lastUsed: ++this.clock });
    this.ledger.record("publish", key, `entries=${this.entries.size}`);
    while (this.entries.size > this.maxEntries) {
      this.evictLeastRecentlyUsed();
    }
  }

  private evictLeastRecentlyUsed(): void {
    let oldestKey: string | undefined;
    let oldestUse = Infinity;
    for (const [key, entry] of this.entries) {
      if (entry.lastUsed < oldestUse) {
        oldestUse = entry.lastUsed;
        oldestKey = key;
      }
    }
    if (oldestKey !== undefined) {
      this.entries.delete(oldestKey);
      this.ledger.record("evict", oldestKey, `quota=${this.maxEntries}`);
    }
  }
}
