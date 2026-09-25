import {
  AttrFetchError,
  CancelledError,
  FetchTimeoutError,
  PolicyError,
  VersionConflictError,
} from './types.ts';
import type {
  AttrKey,
  AttributeSource,
  FetchOutcome,
  ReadSession,
  Subject,
  Version,
} from './types.ts';

interface CacheEntry {
  readonly value: unknown;
  readonly version: Version;
  readonly source: string;
}

interface Waiter {
  readonly session: ReadSession;
  settled: boolean;
  readonly resolve: (v: unknown) => void;
  readonly reject: (e: Error) => void;
  readonly onAbort: () => void;
}

interface PendingBatch {
  readonly source: AttributeSource;
  readonly subject: Subject;
  readonly permission: string;
  readonly keys: Set<AttrKey>;
  readonly waiters: Map<AttrKey, Waiter[]>;
}

export interface SchedulerOptions {
  readonly fetchTimeoutMs?: number;
}

export class Scheduler {
  private readonly sourcesByAttr: ReadonlyMap<AttrKey, AttributeSource>;
  private readonly fetchTimeoutMs: number;
  private readonly cache = new Map<string, CacheEntry>();
  private readonly pending = new Map<string, PendingBatch>();
  private readonly inFlight = new Map<string, Promise<void>>();

  constructor(sourcesByAttr: ReadonlyMap<AttrKey, AttributeSource>, opts: SchedulerOptions = {}) {
    this.sourcesByAttr = sourcesByAttr;
    this.fetchTimeoutMs = opts.fetchTimeoutMs ?? 0;
  }

  read(session: ReadSession, key: AttrKey): Promise<unknown> {
    if (session.signal.aborted) {
      return Promise.reject(new CancelledError());
    }
    if (session.values.has(key)) {
      return Promise.resolve(session.values.get(key));
    }
    const pendingRead = session.pendingReads.get(key);
    if (pendingRead) {
      return pendingRead;
    }
    const source = this.sourcesByAttr.get(key);
    if (!source) {
      return Promise.reject(new PolicyError(`unknown attribute: ${key}`));
    }
    const hit = this.cache.get(this.cacheKey(session.permission, session.subject.id, key));
    if (hit) {
      try {
        return Promise.resolve(this.adopt(session, source, hit.version, key, hit.value));
      } catch (err) {
        return Promise.reject(err as Error);
      }
    }
    const p = this.enqueue(session, source, key);
    session.pendingReads.set(key, p);
    return p;
  }

  invalidate(permission: string, subjectId: string, sourceName: string): void {
    const prefix = this.cachePrefix(permission, subjectId);
    for (const [k, v] of this.cache) {
      if (k.startsWith(prefix) && v.source === sourceName) {
        this.cache.delete(k);
      }
    }
  }

  private adopt(
    session: ReadSession,
    source: AttributeSource,
    version: Version,
    key: AttrKey,
    value: unknown,
  ): unknown {
    const seen = session.versions.get(source.name);
    if (seen !== undefined && seen !== version) {
      throw new VersionConflictError(source.name, seen, version);
    }
    session.versions.set(source.name, version);
    session.values.set(key, value);
    session.evidence.push({ key, value, version, source: source.name });
    return value;
  }

  private enqueue(session: ReadSession, source: AttributeSource, key: AttrKey): Promise<unknown> {
    const bk = this.batchKey(session.permission, session.subject.id, source.name);
    let pb = this.pending.get(bk);
    if (!pb) {
      pb = {
        source,
        subject: session.subject,
        permission: session.permission,
        keys: new Set(),
        waiters: new Map(),
      };
      this.pending.set(bk, pb);
    }
    const batch = pb;
    return new Promise<unknown>((resolve, reject) => {
      const waiter: Waiter = {
        session,
        settled: false,
        resolve,
        reject,
        onAbort: () => {
          if (waiter.settled) {
            return;
          }
          waiter.settled = true;
          const list = batch.waiters.get(key);
          if (list) {
            const i = list.indexOf(waiter);
            if (i >= 0) {
              list.splice(i, 1);
            }
          }
          reject(new CancelledError());
        },
      };
      if (session.signal.aborted) {
        reject(new CancelledError());
        return;
      }
      session.signal.addEventListener('abort', waiter.onAbort, { once: true });
      batch.keys.add(key);
      const list = batch.waiters.get(key);
      if (list) {
        list.push(waiter);
      } else {
        batch.waiters.set(key, [waiter]);
      }
      this.scheduleFlush(bk);
    });
  }

  private scheduleFlush(bk: string): void {
    queueMicrotask(() => this.flush(bk));
  }

  private flush(bk: string): void {
    const pb = this.pending.get(bk);
    if (!pb) {
      return;
    }
    const flying = this.inFlight.get(bk);
    if (flying) {
      // Reentrancy-safe, deterministic: a batch requested while another batch
      // for the same (permission, subject, source) is in flight is served by a
      // later batch, after the current one settles.
      void flying.then(() => this.scheduleFlush(bk));
      return;
    }
    this.pending.delete(bk);
    const keys = [...pb.keys].filter((k) => (pb.waiters.get(k) ?? []).some((w) => !w.settled));
    if (keys.length === 0) {
      return;
    }
    const flight = this.dispatch(pb, keys);
    this.inFlight.set(bk, flight);
    void flight.then(() => {
      if (this.inFlight.get(bk) === flight) {
        this.inFlight.delete(bk);
      }
    });
  }

  private async dispatch(pb: PendingBatch, keys: readonly AttrKey[]): Promise<void> {
    let outcome: FetchOutcome;
    try {
      outcome = await this.withTimeout(
        pb.source.fetchBatch({ subject: pb.subject, permission: pb.permission, keys }),
        this.fetchTimeoutMs,
      );
    } catch (err) {
      const error = err instanceof Error ? err : new Error(String(err));
      for (const k of keys) {
        for (const w of pb.waiters.get(k) ?? []) {
          this.settle(w, () => w.reject(error));
        }
      }
      return;
    }
    const failures = outcome.failures ?? new Map<AttrKey, Error>();
    for (const k of keys) {
      const ws = pb.waiters.get(k) ?? [];
      const failure = failures.get(k);
      if (failure !== undefined) {
        for (const w of ws) {
          this.settle(w, () => w.reject(new AttrFetchError(k, failure)));
        }
        continue;
      }
      if (!outcome.values.has(k)) {
        for (const w of ws) {
          this.settle(w, () =>
            w.reject(new AttrFetchError(k, new Error('attribute missing from batch outcome'))),
          );
        }
        continue;
      }
      const value = outcome.values.get(k);
      this.cache.set(this.cacheKey(pb.permission, pb.subject.id, k), {
        value,
        version: outcome.version,
        source: pb.source.name,
      });
      for (const w of ws) {
        this.settle(w, () => {
          try {
            w.resolve(this.adopt(w.session, pb.source, outcome.version, k, value));
          } catch (err) {
            w.reject(err as Error);
          }
        });
      }
    }
  }

  private settle(w: Waiter, fn: () => void): void {
    if (w.settled) {
      return;
    }
    w.settled = true;
    w.session.signal.removeEventListener('abort', w.onAbort);
    fn();
  }

  private withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
    if (!ms || ms <= 0) {
      return p;
    }
    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => reject(new FetchTimeoutError(ms)), ms);
      p.then(
        (v) => {
          clearTimeout(timer);
          resolve(v);
        },
        (e) => {
          clearTimeout(timer);
          reject(e instanceof Error ? e : new Error(String(e)));
        },
      );
    });
  }

  private cacheKey(permission: string, subjectId: string, attr: AttrKey): string {
    return JSON.stringify([permission, subjectId, attr]);
  }

  private cachePrefix(permission: string, subjectId: string): string {
    return JSON.stringify([permission, subjectId]).slice(0, -1);
  }

  private batchKey(permission: string, subjectId: string, sourceName: string): string {
    return JSON.stringify([permission, subjectId, sourceName]);
  }
}
