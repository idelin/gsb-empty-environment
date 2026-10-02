import { Normalizer } from './normalize.ts';
import type { NormConfig } from './normalize.ts';
import { applyOps, emptyState, readEntity, resolveAlias } from './state.ts';
import type { Op, State } from './state.ts';
import { deepEqual, isRef } from './types.ts';
import type { EntityKey, Ref, Value } from './types.ts';

/** Read facade handed to live queries; every read records a lease. */
export interface Reader {
  get(key: EntityKey): Record<string, Value> | undefined;
  denormalize(refOrKey: Ref | EntityKey): unknown;
  keysOfType(type: string): EntityKey[];
}

export interface Transaction {
  ingest(type: string, data: unknown, opts?: { version?: number }): Value;
  update(key: EntityKey, fields: Record<string, unknown>, opts?: { version?: number }): void;
  delete(key: EntityKey, opts?: { version?: number }): void;
  mergeIdentities(from: EntityKey, to: EntityKey): void;
  appendConnection(
    key: EntityKey,
    field: string,
    items: unknown[],
    pageInfo?: Record<string, unknown>,
    opts?: { version?: number; at?: 'start' | 'end' },
  ): void;
  /** Read-your-writes: sees ops already buffered in this transaction. */
  get(key: EntityKey): Record<string, Value> | undefined;
}

function denormValue(
  state: State,
  v: Value,
  leases: Set<string> | null,
  seen: Set<EntityKey>,
): unknown {
  if (isRef(v)) return denorm(state, v.$ref, leases, seen);
  if (Array.isArray(v)) return v.map((x) => denormValue(state, x, leases, seen));
  if (v !== null && typeof v === 'object') {
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(v)) {
      out[k] = denormValue(state, (v as Record<string, Value>)[k], leases, seen);
    }
    return out;
  }
  return v;
}

function denorm(
  state: State,
  key: EntityKey,
  leases: Set<string> | null,
  seen: Set<EntityKey>,
): unknown {
  const resolved = resolveAlias(state, key);
  if (leases) leases.add(resolved);
  if (seen.has(resolved)) return { $ref: resolved }; // reference cycle guard
  const fields = readEntity(state, resolved);
  if (!fields) return null;
  seen.add(resolved);
  const out: Record<string, unknown> = {};
  for (const f of Object.keys(fields)) {
    out[f] = denormValue(state, fields[f], leases, seen);
  }
  seen.delete(resolved);
  return out;
}

function makeReader(state: State, leases: Set<string> | null): Reader {
  return {
    get(key) {
      const resolved = resolveAlias(state, key);
      if (leases) leases.add(resolved);
      return readEntity(state, resolved);
    },
    denormalize(refOrKey) {
      const key = typeof refOrKey === 'string' ? refOrKey : refOrKey.$ref;
      return denorm(state, key, leases, new Set());
    },
    keysOfType(type) {
      if (leases) leases.add(`${type}:*`);
      const out: EntityKey[] = [];
      for (const k of state.entities.keys()) {
        if (k.startsWith(`${type}:`)) out.push(k);
      }
      return out.sort();
    },
  };
}

/** An immutable view of the store at a specific commit. */
export class Snapshot {
  private readonly state: State;
  constructor(state: State) {
    this.state = state;
  }
  get seq(): number {
    return this.state.seq;
  }
  get(key: EntityKey): Record<string, Value> | undefined {
    return readEntity(this.state, key);
  }
  denormalize(refOrKey: Ref | EntityKey): unknown {
    const key = typeof refOrKey === 'string' ? refOrKey : refOrKey.$ref;
    return denorm(this.state, key, null, new Set());
  }
  keysOfType(type: string): EntityKey[] {
    return makeReader(this.state, null).keysOfType(type);
  }
  toJSON(): unknown {
    const entities: Record<string, unknown> = {};
    for (const k of [...this.state.entities.keys()].sort()) {
      const rec = this.state.entities.get(k)!;
      entities[k] = { fields: rec.fields, versions: rec.versions };
    }
    const reverse: Record<string, unknown> = {};
    for (const k of [...this.state.reverse.keys()].sort()) {
      reverse[k] = [...this.state.reverse.get(k)!.entries()]
        .map(([s, n]) => `${s} x${n}`)
        .sort();
    }
    return {
      entities,
      reverse,
      tombstones: [...this.state.tombstones.entries()].sort(),
      aliases: [...this.state.aliases.entries()].sort(),
      layers: this.state.layers.map((l) => ({
        id: l.id,
        version: l.version,
        writes: [...l.writes.keys()].sort(),
        deletes: [...l.deletes].sort(),
      })),
      clock: this.state.clock,
    };
  }
}

/**
 * A live query. The set of entity keys read during evaluation forms its
 * lease; after every commit, if the changed keys intersect the lease the
 * query re-evaluates and notifies subscribers when the result changed.
 */
export class LiveQuery<R> {
  result!: R;
  private leases: Set<string> = new Set();
  private readonly listeners = new Set<(r: R) => void>();
  private disposed = false;
  private readonly store: Store;
  private readonly fn: (read: Reader) => R;

  constructor(store: Store, fn: (read: Reader) => R) {
    this.store = store;
    this.fn = fn;
    this.recompute();
    store.registerQuery(this);
  }

  private recompute(): void {
    const leases = new Set<string>();
    this.result = this.fn(makeReader(this.store.currentState(), leases));
    this.leases = leases;
  }

  /** Called by the store after each commit. */
  maybeNotify(changed: Set<EntityKey>): void {
    if (this.disposed) return;
    for (const k of changed) {
      const typeWildcard = `${k.slice(0, k.indexOf(':'))}:*`;
      if (this.leases.has(k) || this.leases.has(typeWildcard)) {
        const prev = this.result;
        this.recompute();
        if (!deepEqual(prev, this.result)) {
          for (const l of [...this.listeners]) l(this.result);
        }
        return;
      }
    }
  }

  get leaseKeys(): readonly string[] {
    return [...this.leases].sort();
  }

  subscribe(fn: (r: R) => void): () => void {
    this.listeners.add(fn);
    return () => void this.listeners.delete(fn);
  }

  dispose(): void {
    this.disposed = true;
    this.store.unregisterQuery(this);
  }
}

export class Store {
  private state: State = emptyState();
  private readonly journal: Op[] = [];
  private readonly queries = new Set<LiveQuery<unknown>>();
  private readonly cfg?: Partial<NormConfig>;
  private txDepth = 0;
  private txOps: Op[] = [];
  private txPreview: State | null = null;
  private layerSeq = 0;

  constructor(cfg?: Partial<NormConfig>) {
    this.cfg = cfg;
  }

  /** Rebuild a store by replaying a recorded operation journal. */
  static replay(journal: readonly Op[], cfg?: Partial<NormConfig>): Store {
    const s = new Store(cfg);
    s.commitMany(journal.slice());
    return s;
  }

  /** The recorded operations; replaying them reproduces this exact state. */
  getJournal(): readonly Op[] {
    return this.journal.slice();
  }

  currentState(): State {
    return this.state;
  }

  registerQuery(q: LiveQuery<unknown>): void {
    this.queries.add(q);
  }
  unregisterQuery(q: LiveQuery<unknown>): void {
    this.queries.delete(q);
  }

  private nextVersion(): number {
    return this.readState().clock + 1;
  }

  private readState(): State {
    if (this.txDepth > 0) {
      if (!this.txPreview) this.txPreview = applyOps(this.state, this.txOps).state;
      return this.txPreview;
    }
    return this.state;
  }

  private addOps(ops: Op[]): void {
    if (ops.length === 0) return;
    if (this.txDepth > 0) {
      this.txOps.push(...ops);
      this.txPreview = null;
    } else {
      this.commitMany(ops);
    }
  }

  private commitMany(ops: Op[]): void {
    if (ops.length === 0) return;
    const { state, changed } = applyOps(this.state, ops);
    this.state = state;
    this.journal.push(...ops);
    if (changed.size > 0) {
      for (const q of [...this.queries]) q.maybeNotify(changed);
    }
  }

  /**
   * Run a batch of mutations atomically. Nothing is visible to readers until
   * the callback returns; if it throws, every buffered op is discarded.
   */
  tx<T>(fn: (tx: Transaction) => T): T {
    this.txDepth++;
    const facade: Transaction = {
      ingest: (t, d, o) => this.ingest(t, d, o),
      update: (k, f, o) => this.update(k, f, o),
      delete: (k, o) => this.delete(k, o),
      mergeIdentities: (f, t) => this.mergeIdentities(f, t),
      appendConnection: (k, f, items, pi, o) => this.appendConnection(k, f, items, pi, o),
      get: (k) => readEntity(this.readState(), k),
    };
    try {
      const result = fn(facade);
      this.txDepth--;
      if (this.txDepth === 0) {
        const ops = this.txOps;
        this.txOps = [];
        this.txPreview = null;
        this.commitMany(ops);
      }
      return result;
    } catch (e) {
      this.txDepth--;
      if (this.txDepth === 0) {
        this.txOps = [];
        this.txPreview = null;
      }
      throw e;
    }
  }

  /** Normalize a nested response into the entity tables. */
  ingest(type: string, data: unknown, opts: { version?: number } = {}): Value {
    const version = opts.version ?? this.nextVersion();
    const n = new Normalizer(this.cfg);
    const root = n.rootOf(type, data);
    const ops: Op[] = [...n.patches].map(([key, fields]) => ({
      kind: 'upsert',
      key,
      fields,
      version,
    }));
    this.addOps(ops);
    return root;
  }

  /** Partial field update; absent fields keep their previous values. */
  update(
    key: EntityKey,
    fields: Record<string, unknown>,
    opts: { version?: number } = {},
  ): void {
    const version = opts.version ?? this.nextVersion();
    const n = new Normalizer(this.cfg);
    const own = n.fieldsOf(fields);
    const ops: Op[] = [...n.patches].map(([k, f]) => ({
      kind: 'upsert',
      key: k,
      fields: f,
      version,
    }));
    ops.push({ kind: 'upsert', key, fields: own, version });
    this.addOps(ops);
  }

  /** Explicit delete: tombstones the entity and cleans the reverse index. */
  delete(key: EntityKey, opts: { version?: number } = {}): void {
    this.addOps([{ kind: 'delete', key, version: opts.version ?? this.nextVersion() }]);
  }

  /**
   * Merge two identities (e.g. an optimistic temp id and the server-assigned
   * id). Fields merge per-version and every referrer is atomically retargeted.
   */
  mergeIdentities(from: EntityKey, to: EntityKey): void {
    this.addOps([{ kind: 'mergeIdentity', from, to, version: this.nextVersion() }]);
  }

  /** Append (or prepend) a page of a paginated relation, deduping by ref. */
  appendConnection(
    key: EntityKey,
    field: string,
    items: unknown[],
    pageInfo: Record<string, unknown> = {},
    opts: { version?: number; at?: 'start' | 'end' } = {},
  ): void {
    const version = opts.version ?? this.nextVersion();
    const n = new Normalizer(this.cfg);
    const refs = items.map((item) => n.value(item) ?? null);
    const ops: Op[] = [...n.patches].map(([k, f]) => ({
      kind: 'upsert',
      key: k,
      fields: f,
      version,
    }));
    const current = readEntity(this.readState(), key)?.[field];
    const curObj =
      current !== null && typeof current === 'object' && !Array.isArray(current) && !isRef(current)
        ? (current as Record<string, Value>)
        : {};
    const existing = Array.isArray(curObj.items) ? curObj.items : [];
    const fresh = refs.filter(
      (r) =>
        !existing.some((e) =>
          isRef(e) && isRef(r) ? e.$ref === r.$ref : deepEqual(e, r),
        ),
    );
    const merged =
      opts.at === 'start' ? [...fresh, ...existing] : [...existing, ...fresh];
    const curInfo =
      curObj.pageInfo !== null &&
      typeof curObj.pageInfo === 'object' &&
      !Array.isArray(curObj.pageInfo)
        ? (curObj.pageInfo as Record<string, Value>)
        : {};
    const value: Record<string, Value> = {
      ...curObj,
      items: merged,
      pageInfo: { ...curInfo, ...(n.fieldsOf(pageInfo) as Record<string, Value>) },
    };
    ops.push({ kind: 'upsert', key, fields: { [field]: value }, version });
    this.addOps(ops);
  }

  /** Begin an optimistic overlay layer; returns its id and field version. */
  beginOptimistic(): { layerId: string; version: number } {
    const version = this.nextVersion();
    const layerId = `optimistic-${++this.layerSeq}`;
    this.addOps([{ kind: 'layerBegin', layerId, version }]);
    return { layerId, version };
  }

  writeOptimistic(layerId: string, key: EntityKey, fields: Record<string, unknown>): void {
    const n = new Normalizer(this.cfg);
    const own = n.fieldsOf(fields);
    const ops: Op[] = [...n.patches].map(([k, f]) => ({
      kind: 'layerWrite',
      layerId,
      key: k,
      fields: f,
    }));
    ops.push({ kind: 'layerWrite', layerId, key, fields: own });
    this.addOps(ops);
  }

  deleteOptimistic(layerId: string, key: EntityKey): void {
    this.addOps([{ kind: 'layerDelete', layerId, key }]);
  }

  /**
   * Confirm a layer with the server's response. The response is committed to
   * the base state at the layer's version — so a stale ack can never overwrite
   * a newer user-submitted field version — and the layer is dropped in the
   * same atomic commit.
   */
  commitLayer(layerId: string, confirm?: { type: string; data: unknown }): void {
    const layer = this.readState().layers.find((l) => l.id === layerId);
    if (!layer) throw new Error(`unknown optimistic layer: ${layerId}`);
    const ops: Op[] = [];
    // The layer's own writes are accepted into the base at the layer version.
    for (const [key, fields] of layer.writes) {
      ops.push({
        kind: 'upsert',
        key,
        fields: Object.fromEntries(fields),
        version: layer.version,
      });
    }
    if (confirm) {
      const n = new Normalizer(this.cfg);
      n.rootOf(confirm.type, confirm.data);
      for (const [key, fields] of n.patches) {
        ops.push({ kind: 'upsert', key, fields, version: layer.version });
      }
    }
    ops.push({ kind: 'layerCommit', layerId });
    this.addOps(ops);
  }

  /** Roll back a layer: only its own writes disappear. */
  rollbackLayer(layerId: string): void {
    this.addOps([{ kind: 'layerRollback', layerId }]);
  }

  // Public reads always observe committed state; a transaction's buffered
  // ops are only visible through the Transaction facade's own `get`.
  get(key: EntityKey): Record<string, Value> | undefined {
    return readEntity(this.state, key);
  }

  has(key: EntityKey): boolean {
    return this.get(key) !== undefined;
  }

  denormalize(refOrKey: Ref | EntityKey): unknown {
    const key = typeof refOrKey === 'string' ? refOrKey : refOrKey.$ref;
    return denorm(this.state, key, null, new Set());
  }

  snapshot(): Snapshot {
    return new Snapshot(this.state);
  }

  query<R>(fn: (read: Reader) => R): LiveQuery<R> {
    return new LiveQuery<R>(this, fn);
  }
}
