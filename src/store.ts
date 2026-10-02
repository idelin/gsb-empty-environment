import { isConnection, isRef, splitKey } from './types.ts';
import type {
  Connection,
  EntityKey,
  FieldEntry,
  Op,
  PageInfo,
  Value,
} from './types.ts';
import { State, applyOp, serializeState } from './state.ts';
import { normalizeEntityInto, normalizeValueInto } from './normalize.ts';
import type { FieldWriter } from './normalize.ts';

export interface Lease {
  id: string;
  release(): void;
}

export class Transaction {
  private store: Store;
  private working: State;
  private ops: Op[] = [];
  private closed = false;

  constructor(store: Store, base: State) {
    this.store = store;
    this.working = base.clone();
  }

  private get nextVersion(): number {
    return this.working.clock + 1;
  }

  private apply(op: Op): void {
    if (this.closed) throw new Error('transaction is closed');
    applyOp(this.working, op);
    this.ops.push(op);
  }

  /** Unguarded user write: always wins, stamped with the transaction version. */
  writeField(key: EntityKey, field: string, value: Value): void {
    this.apply({ op: 'writeField', key, field, value, version: this.nextVersion });
  }

  /** User write of raw (possibly nested) data; nested entities are extracted. */
  writeData(key: EntityKey, field: string, raw: unknown): void {
    const version = this.nextVersion;
    const writer: FieldWriter = {
      put: (k, f, v) => this.apply({ op: 'writeField', key: k, field: f, value: v, version }),
    };
    const value = normalizeValueInto(writer, raw);
    this.apply({ op: 'writeField', key, field, value, version });
  }

  /**
   * Apply a (possibly partial) server response. `baseVersion` is the logical
   * clock at which the request was issued; fields the user has since written
   * at a newer version are not overwritten by this stale payload.
   */
  applyResponse(obj: Record<string, unknown>, opts: { baseVersion?: number } = {}): EntityKey {
    const baseVersion = opts.baseVersion ?? this.nextVersion;
    const writer: FieldWriter = {
      put: (k, f, v) => {
        const existing = this.working.entities.get(k)?.fields.get(f);
        if (existing && existing.version > baseVersion) return; // stale response
        this.apply({ op: 'writeField', key: k, field: f, value: v, version: baseVersion });
      },
    };
    return normalizeEntityInto(writer, obj);
  }

  /** Merge a page of a paginated relation; dedups refs, keeps latest pageInfo. */
  applyPage(
    key: EntityKey,
    field: string,
    nodes: unknown[],
    pageInfo: PageInfo,
    opts: { baseVersion?: number; reset?: boolean } = {},
  ): void {
    const baseVersion = opts.baseVersion ?? this.nextVersion;
    const existing = this.working.entities.get(key)?.fields.get(field);
    if (existing && existing.version > baseVersion) return; // stale page
    const writer: FieldWriter = {
      put: (k, f, v) =>
        this.apply({ op: 'writeField', key: k, field: f, value: v, version: baseVersion }),
    };
    const newRefs = nodes.map((node) => {
      const value = normalizeValueInto(writer, node);
      if (!isRef(value)) throw new Error('page nodes must be entities with __typename and id');
      return value;
    });
    let refs = newRefs;
    if (!opts.reset && existing && isConnection(existing.value)) {
      const seen = new Set(existing.value.refs.map((r) => r.$ref));
      refs = [...existing.value.refs, ...newRefs.filter((r) => !seen.has(r.$ref))];
    }
    const value: Connection = { kind: 'connection', refs, pageInfo };
    this.apply({ op: 'writeField', key, field, value, version: baseVersion });
  }

  deleteEntities(keys: EntityKey[]): void {
    this.apply({ op: 'deleteEntities', keys: [...keys] });
  }

  deleteEntity(key: EntityKey): void {
    this.deleteEntities([key]);
  }

  mergeIdentity(typename: string, fromId: string | number, toId: string | number): void {
    this.apply({
      op: 'mergeIdentity',
      typename,
      fromId: String(fromId),
      toId: String(toId),
      version: this.nextVersion,
    });
  }

  commit(): void {
    if (this.closed) throw new Error('transaction is closed');
    this.apply({ op: 'tick' });
    this.store.commitBase(this.working, this.ops);
    this.closed = true;
  }

  /** Discards every change made in this transaction; nothing else is touched. */
  rollback(): void {
    if (this.closed) throw new Error('transaction is closed');
    this.closed = true;
    this.store.endTransaction();
  }
}

export class OptimisticLayer {
  private store: Store;
  private closed = false;
  readonly writes: Map<EntityKey, Map<string, FieldEntry>> = new Map();
  readonly deletes: Set<EntityKey> = new Set();

  constructor(store: Store) {
    this.store = store;
  }

  private put(key: EntityKey, field: string, value: Value): void {
    if (this.closed) throw new Error('layer is closed');
    let fields = this.writes.get(key);
    if (!fields) this.writes.set(key, (fields = new Map()));
    fields.set(field, { value, version: Number.MAX_SAFE_INTEGER });
  }

  writeField(key: EntityKey, field: string, value: Value): void {
    this.put(key, field, value);
  }

  writeData(key: EntityKey, field: string, raw: unknown): void {
    const writer: FieldWriter = { put: (k, f, v) => this.put(k, f, v) };
    const value = normalizeValueInto(writer, raw);
    this.put(key, field, value);
  }

  deleteEntity(key: EntityKey): void {
    if (this.closed) throw new Error('layer is closed');
    this.deletes.add(key);
  }

  /** Folds this layer into the committed base as one atomic version step. */
  commit(): void {
    if (this.closed) throw new Error('layer is closed');
    this.closed = true;
    this.store.commitLayer(this);
  }

  /** Drops only this layer's changes; other layers and the base are kept. */
  rollback(): void {
    if (this.closed) throw new Error('layer is closed');
    this.closed = true;
    this.store.dropLayer(this);
  }
}

export class Store {
  private base: State = new State();
  private log: Op[] = [];
  private layers: OptimisticLayer[] = [];
  private transactionOpen = false;
  private leaseSeq = 0;

  static replay(ops: Op[]): Store {
    const store = new Store();
    const state = new State();
    for (const op of ops) applyOp(state, op);
    store.base = state;
    store.log = [...ops];
    return store;
  }

  get clock(): number {
    return this.base.clock;
  }

  get opLog(): Op[] {
    return [...this.log];
  }

  /** Committed state. Never mutated in place; commits swap it atomically. */
  snapshot(): State {
    return this.base;
  }

  begin(): Transaction {
    if (this.transactionOpen) throw new Error('a transaction is already open');
    this.transactionOpen = true;
    return new Transaction(this, this.base);
  }

  commitBase(state: State, ops: Op[]): void {
    this.base = state;
    this.log.push(...ops);
    this.transactionOpen = false;
  }

  endTransaction(): void {
    this.transactionOpen = false;
  }

  optimistic(): OptimisticLayer {
    const layer = new OptimisticLayer(this);
    this.layers.push(layer);
    return layer;
  }

  commitLayer(layer: OptimisticLayer): void {
    const working = this.base.clone();
    const ops: Op[] = [];
    const version = working.clock + 1;
    for (const [key, fields] of layer.writes) {
      for (const [field, entry] of fields) {
        const op: Op = { op: 'writeField', key, field, value: entry.value, version };
        applyOp(working, op);
        ops.push(op);
      }
    }
    if (layer.deletes.size > 0) {
      const op: Op = { op: 'deleteEntities', keys: [...layer.deletes] };
      applyOp(working, op);
      ops.push(op);
    }
    applyOp(working, { op: 'tick' });
    ops.push({ op: 'tick' });
    this.base = working;
    this.log.push(...ops);
    this.dropLayer(layer);
  }

  dropLayer(layer: OptimisticLayer): void {
    this.layers = this.layers.filter((l) => l !== layer);
  }

  acquireLease(keys: EntityKey[]): Lease {
    const leaseId = `lease-${++this.leaseSeq}`;
    this.applyDirect({ op: 'acquireLease', leaseId, keys: [...keys] });
    return {
      id: leaseId,
      release: () => this.applyDirect({ op: 'releaseLease', leaseId }),
    };
  }

  private applyDirect(op: Op): void {
    const working = this.base.clone();
    applyOp(working, op);
    this.base = working;
    this.log.push(op);
  }

  // ---- read path (committed base overlaid with optimistic layers) ----

  private layeredFieldEntry(key: EntityKey, field: string): FieldEntry | undefined {
    for (let i = this.layers.length - 1; i >= 0; i--) {
      const layer = this.layers[i];
      if (layer.deletes.has(key)) return undefined;
      const entry = layer.writes.get(key)?.get(field);
      if (entry) return entry;
    }
    return this.base.entities.get(key)?.fields.get(field);
  }

  hasEntity(key: EntityKey): boolean {
    if (!this.base.entities.has(key)) return false;
    return !this.layers.some((l) => l.deletes.has(key));
  }

  hasField(key: EntityKey, field: string): boolean {
    return this.layeredFieldEntry(key, field) !== undefined;
  }

  /** undefined = field missing; null = explicitly set to null. */
  getField(key: EntityKey, field: string): Value | undefined {
    return this.layeredFieldEntry(key, field)?.value;
  }

  entityKeys(): EntityKey[] {
    const keys = new Set<EntityKey>(this.base.entities.keys());
    for (const layer of this.layers) {
      for (const key of layer.deletes) keys.delete(key);
      for (const key of layer.writes.keys()) keys.add(key);
    }
    return [...keys].sort();
  }

  /** Inbound references to `key`: source key -> fields pointing at it. */
  inboundRefs(key: EntityKey): Record<string, string[]> {
    const out: Record<string, string[]> = {};
    const sources = this.base.reverseIndex.get(key);
    if (sources) for (const [source, fields] of sources) out[source] = [...fields].sort();
    return out;
  }

  readEntity(key: EntityKey): Record<string, unknown> | undefined {
    if (!this.hasEntity(key)) return undefined;
    return this.denormalizeEntity(key, new Set([key]));
  }

  private denormalizeEntity(key: EntityKey, seen: Set<EntityKey>): Record<string, unknown> {
    const { typename, id } = splitKey(key);
    const out: Record<string, unknown> = { __typename: typename, id };
    const fieldNames = new Set<string>();
    const record = this.base.entities.get(key);
    if (record) for (const f of record.fields.keys()) fieldNames.add(f);
    for (const layer of this.layers) {
      const writes = layer.writes.get(key);
      if (writes) for (const f of writes.keys()) fieldNames.add(f);
    }
    for (const field of fieldNames) {
      const entry = this.layeredFieldEntry(key, field);
      if (entry) out[field] = this.denormalizeValue(entry.value, seen);
    }
    return out;
  }

  private denormalizeValue(value: Value, seen: Set<EntityKey>): unknown {
    if (isRef(value)) {
      if (!this.hasEntity(value.$ref)) return null;
      if (seen.has(value.$ref)) {
        const { typename, id } = splitKey(value.$ref);
        return { __typename: typename, id }; // cycle guard: identity stub
      }
      return this.denormalizeEntity(value.$ref, new Set([...seen, value.$ref]));
    }
    if (isConnection(value)) {
      return {
        nodes: value.refs.map((r) => this.denormalizeValue(r, seen)),
        pageInfo: value.pageInfo,
      };
    }
    if (Array.isArray(value)) return value.map((v) => this.denormalizeValue(v, seen));
    if (value !== null && typeof value === 'object') {
      const out: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(value)) out[k] = this.denormalizeValue(v, seen);
      return out;
    }
    return value;
  }

  serialize(): unknown {
    return serializeState(this.base);
  }
}
