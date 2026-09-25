/**
 * Cost-aware lazy attribute fetch scheduler.
 *
 * - Sources are batch-requested: attribute requests made in the same tick
 *   for the same (subject, scope) are merged into one fetch call.
 * - Cache is merged per (subject, scope); different scopes (permission
 *   contexts) are fully isolated.
 * - Concurrent evaluations share in-flight fetches; one consumer cancelling
 *   does not disturb the others (the underlying fetch is aborted only when
 *   its last consumer detaches).
 * - Every fetch result carries a version. An evaluation that observed
 *   mixed versions retries the stale attributes; after maxVersionRetries
 *   it fails deterministically with VersionConflictError.
 * - Deterministic fetcher behaviour: reentrant request for an in-flight
 *   attribute -> ReentrantFetchError; timeout -> FetchTimeoutError;
 *   per-attribute batch failures are retried, then surface as
 *   AttributeUnavailableError.
 * - Once an evaluation completes or is cancelled, no further attributes
 *   are fetched for it.
 */
import { applyCond, type Policy } from './policy.ts';
import { orderedChildren, type CostTable } from './planner.ts';
import type { Access, Decision, EvidenceStore } from './evidence.ts';

export interface FetchResult {
  version: number;
  values: Record<string, number | { error: string }>;
}

export interface AttrSource {
  fetch(subject: string, attrs: string[], scope: string, signal: AbortSignal): Promise<FetchResult>;
}

export class CancelledError extends Error {
  constructor(message = 'evaluation cancelled') {
    super(message);
    this.name = 'CancelledError';
  }
}

export class FetchTimeoutError extends Error {
  readonly attrs: string[];
  constructor(attrs: string[]) {
    super(`fetch timed out for attributes: ${attrs.join(', ')}`);
    this.name = 'FetchTimeoutError';
    this.attrs = attrs;
  }
}

export class AttributeUnavailableError extends Error {
  readonly attrs: string[];
  constructor(attrs: string[]) {
    super(`attributes unavailable after retries: ${attrs.join(', ')}`);
    this.name = 'AttributeUnavailableError';
    this.attrs = attrs;
  }
}

export class VersionConflictError extends Error {
  constructor() {
    super('could not obtain a version-consistent read within the retry budget');
    this.name = 'VersionConflictError';
  }
}

export class ReentrantFetchError extends Error {
  readonly attr: string;
  constructor(attr: string) {
    super(`reentrant fetch of in-flight attribute: ${attr}`);
    this.name = 'ReentrantFetchError';
    this.attr = attr;
  }
}

interface CachedValue {
  value: number;
  version: number;
}

interface Entry {
  key: string;
  attr: string;
  scope: string;
  subject: string;
  state: 'pending' | 'fetching';
  consumers: number;
  promise: Promise<CachedValue>;
  resolve: (v: CachedValue) => void;
  reject: (e: Error) => void;
  batch: Batch | null;
}

interface Batch {
  entries: Entry[];
  controller: AbortController;
}

export interface SchedulerOptions {
  costs: CostTable;
  fetchTimeoutMs?: number;
  maxPartialRetries?: number;
  maxVersionRetries?: number;
}

export interface EvaluateOptions {
  signal?: AbortSignal;
  decisionId?: string;
}

export class AttributeScheduler {
  private readonly source: AttrSource;
  private readonly evidence: EvidenceStore;
  private readonly costs: CostTable;
  private readonly fetchTimeoutMs: number;
  private readonly maxPartialRetries: number;
  private readonly maxVersionRetries: number;
  private readonly cache = new Map<string, CachedValue>();
  private readonly entries = new Map<string, Entry>();
  private flushScheduled = false;
  private activeBatch: ReadonlySet<string> | null = null;

  constructor(source: AttrSource, evidence: EvidenceStore, opts: SchedulerOptions) {
    this.source = source;
    this.evidence = evidence;
    this.costs = opts.costs;
    this.fetchTimeoutMs = opts.fetchTimeoutMs ?? 1000;
    this.maxPartialRetries = opts.maxPartialRetries ?? 2;
    this.maxVersionRetries = opts.maxVersionRetries ?? 3;
  }

  static keyOf(scope: string, subject: string, attr: string): string {
    return `${scope}::${subject}::${attr}`;
  }

  async get(subject: string, scope: string, attr: string, signal?: AbortSignal): Promise<CachedValue> {
    if (signal?.aborted) throw new CancelledError();
    const key = AttributeScheduler.keyOf(scope, subject, attr);
    const hit = this.cache.get(key);
    if (hit) return hit;
    if (this.activeBatch?.has(key)) throw new ReentrantFetchError(attr);
    let entry = this.entries.get(key);
    if (!entry) {
      entry = this.createEntry(key, attr, scope, subject);
      this.scheduleFlush();
    }
    return this.acquire(entry, signal);
  }

  async evaluate(policy: Policy, subject: string, scope: string, opts: EvaluateOptions = {}): Promise<Decision> {
    for (let attempt = 0; ; attempt++) {
      const accesses = new Map<string, Access>();
      const allow = await this.evalNode(policy, subject, scope, accesses, opts.signal);
      const versions = new Set([...accesses.values()].map((a) => a.version));
      if (versions.size <= 1) {
        const decision: Decision = {
          id: opts.decisionId ?? `d-${Date.now()}-${Math.random().toString(36).slice(2)}`,
          subject,
          scope,
          allow,
          accesses: [...accesses.values()],
          decidedAt: Date.now(),
        };
        await this.evidence.commit(decision); // decision + evidence saved atomically
        return decision;
      }
      if (attempt >= this.maxVersionRetries) throw new VersionConflictError();
      const maxVersion = Math.max(...[...accesses.values()].map((a) => a.version));
      for (const a of accesses.values()) {
        if (a.version < maxVersion) this.cache.delete(AttributeScheduler.keyOf(scope, subject, a.attr));
      }
    }
  }

  private async evalNode(
    node: Policy,
    subject: string,
    scope: string,
    accesses: Map<string, Access>,
    signal?: AbortSignal,
  ): Promise<boolean> {
    switch (node.kind) {
      case 'cmp':
      case 'in': {
        const { value, version } = await this.get(subject, scope, node.attr, signal);
        if (!accesses.has(node.attr)) accesses.set(node.attr, { attr: node.attr, value, version });
        return applyCond(node, value);
      }
      case 'not':
        return !(await this.evalNode(node.child, subject, scope, accesses, signal));
      case 'and': {
        for (const child of orderedChildren(node.children, this.costs)) {
          if (!(await this.evalNode(child, subject, scope, accesses, signal))) return false;
        }
        return true;
      }
      case 'or': {
        for (const child of orderedChildren(node.children, this.costs)) {
          if (await this.evalNode(child, subject, scope, accesses, signal)) return true;
        }
        return false;
      }
    }
  }

  private createEntry(key: string, attr: string, scope: string, subject: string): Entry {
    let resolve!: (v: CachedValue) => void;
    let reject!: (e: Error) => void;
    const promise = new Promise<CachedValue>((res, rej) => {
      resolve = res;
      reject = rej;
    });
    const entry: Entry = {
      key,
      attr,
      scope,
      subject,
      state: 'pending',
      consumers: 0,
      promise,
      resolve,
      reject,
      batch: null,
    };
    this.entries.set(key, entry);
    return entry;
  }

  private acquire(entry: Entry, signal?: AbortSignal): Promise<CachedValue> {
    if (signal?.aborted) return Promise.reject(new CancelledError());
    entry.consumers++;
    return new Promise<CachedValue>((resolve, reject) => {
      const cleanup = (): void => signal?.removeEventListener('abort', onAbort);
      const onAbort = (): void => {
        cleanup();
        this.release(entry);
        reject(new CancelledError());
      };
      signal?.addEventListener('abort', onAbort, { once: true });
      entry.promise.then(
        (v) => {
          cleanup();
          resolve(v);
        },
        (e) => {
          cleanup();
          reject(e);
        },
      );
    });
  }

  private release(entry: Entry): void {
    entry.consumers = Math.max(0, entry.consumers - 1);
    if (entry.consumers > 0) return;
    if (entry.state === 'pending') {
      // Cancelled before the batch ever ran: never fetch it.
      this.entries.delete(entry.key);
      entry.reject(new CancelledError());
    } else if (entry.batch && entry.batch.entries.every((e) => e.consumers === 0)) {
      entry.batch.controller.abort(new CancelledError());
    }
  }

  private scheduleFlush(): void {
    if (this.flushScheduled) return;
    this.flushScheduled = true;
    queueMicrotask(() => {
      this.flushScheduled = false;
      this.flush();
    });
  }

  private flush(): void {
    const byBatch = new Map<string, Entry[]>();
    for (const entry of this.entries.values()) {
      if (entry.state !== 'pending') continue;
      const batchKey = `${entry.scope}::${entry.subject}`;
      const list = byBatch.get(batchKey) ?? [];
      list.push(entry);
      byBatch.set(batchKey, list);
    }
    for (const entries of byBatch.values()) {
      for (const e of entries) e.state = 'fetching';
      const { scope, subject } = entries[0];
      void this.runBatch(scope, subject, entries, this.maxPartialRetries);
    }
  }

  private async runBatch(scope: string, subject: string, entries: Entry[], retriesLeft: number): Promise<void> {
    const attrs = entries.map((e) => e.attr);
    const controller = new AbortController();
    const batch: Batch = { entries, controller };
    for (const e of entries) e.batch = batch;

    const keys = new Set(entries.map((e) => e.key));
    const timeout = setTimeout(() => controller.abort(new FetchTimeoutError(attrs)), this.fetchTimeoutMs);
    let result: FetchResult;
    try {
      const aborted = new Promise<never>((_, rej) => {
        controller.signal.addEventListener('abort', () => rej(controller.signal.reason ?? new CancelledError()), {
          once: true,
        });
      });
      this.activeBatch = keys;
      let fetchPromise: Promise<FetchResult>;
      try {
        fetchPromise = Promise.resolve(this.source.fetch(subject, attrs, scope, controller.signal));
      } finally {
        this.activeBatch = null;
      }
      result = await Promise.race([fetchPromise, aborted]);
    } catch (err) {
      const error = err instanceof Error ? err : new Error(String(err));
      for (const e of entries) this.failEntry(e, error);
      return;
    } finally {
      clearTimeout(timeout);
    }

    const failed: Entry[] = [];
    for (const e of entries) {
      const v = result.values[e.attr];
      if (typeof v === 'number') {
        const cv: CachedValue = { value: v, version: result.version };
        this.cache.set(e.key, cv);
        this.entries.delete(e.key);
        e.resolve(cv);
      } else {
        failed.push(e);
      }
    }
    if (failed.length === 0) return;
    if (retriesLeft > 0) {
      await this.runBatch(scope, subject, failed, retriesLeft - 1);
    } else {
      const err = new AttributeUnavailableError(failed.map((e) => e.attr));
      for (const e of failed) this.failEntry(e, err);
    }
  }

  private failEntry(e: Entry, err: Error): void {
    this.entries.delete(e.key);
    e.reject(err);
  }
}
