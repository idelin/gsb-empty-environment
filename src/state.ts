import { isConnection, isRef, keyOf, splitKey } from './types.ts';
import type { EntityKey, EntityRecord, Op, Value } from './types.ts';

export class State {
  entities: Map<EntityKey, EntityRecord> = new Map();
  // target -> (source -> set of fields on source that reference target)
  reverseIndex: Map<EntityKey, Map<EntityKey, Set<string>>> = new Map();
  leaseCounts: Map<EntityKey, number> = new Map();
  leases: Map<string, Set<EntityKey>> = new Map();
  clock = 0;

  clone(): State {
    const next = new State();
    next.clock = this.clock;
    for (const [key, record] of this.entities) {
      next.entities.set(key, {
        key: record.key,
        typename: record.typename,
        id: record.id,
        pendingDelete: record.pendingDelete,
        fields: new Map(record.fields),
      });
    }
    for (const [target, sources] of this.reverseIndex) {
      const copied = new Map<EntityKey, Set<string>>();
      for (const [source, fields] of sources) copied.set(source, new Set(fields));
      next.reverseIndex.set(target, copied);
    }
    next.leaseCounts = new Map(this.leaseCounts);
    for (const [id, keys] of this.leases) next.leases.set(id, new Set(keys));
    return next;
  }
}

export function refsOf(value: Value): EntityKey[] {
  const out: EntityKey[] = [];
  const walk = (v: Value): void => {
    if (isRef(v)) out.push(v.$ref);
    else if (isConnection(v)) for (const r of v.refs) out.push(r.$ref);
    else if (Array.isArray(v)) v.forEach(walk);
    else if (v !== null && typeof v === 'object') Object.values(v).forEach(walk);
  };
  walk(value);
  return out;
}

export function rewriteRefs(
  value: Value,
  target: EntityKey,
  replacement: EntityKey | null,
): Value {
  if (isRef(value)) {
    if (value.$ref !== target) return value;
    return replacement === null ? null : { $ref: replacement };
  }
  if (isConnection(value)) {
    let refs = value.refs;
    if (replacement === null) {
      refs = refs.filter((r) => r.$ref !== target);
    } else {
      refs = refs.map((r) => (r.$ref === target ? { $ref: replacement } : r));
    }
    const seen = new Set<EntityKey>();
    refs = refs.filter((r) => (seen.has(r.$ref) ? false : (seen.add(r.$ref), true)));
    return { ...value, refs };
  }
  if (Array.isArray(value)) {
    const out: Value[] = [];
    for (const item of value) {
      if (isRef(item) && item.$ref === target && replacement === null) continue;
      out.push(rewriteRefs(item, target, replacement));
    }
    return out;
  }
  if (value !== null && typeof value === 'object') {
    const out: { [field: string]: Value } = {};
    for (const [k, v] of Object.entries(value)) out[k] = rewriteRefs(v, target, replacement);
    return out;
  }
  return value;
}

function indexAdd(state: State, target: EntityKey, source: EntityKey, field: string): void {
  let sources = state.reverseIndex.get(target);
  if (!sources) state.reverseIndex.set(target, (sources = new Map()));
  let fields = sources.get(source);
  if (!fields) sources.set(source, (fields = new Set()));
  fields.add(field);
}

function indexRemove(state: State, target: EntityKey, source: EntityKey, field: string): void {
  const sources = state.reverseIndex.get(target);
  if (!sources) return;
  const fields = sources.get(source);
  if (!fields) return;
  fields.delete(field);
  if (fields.size === 0) sources.delete(source);
  if (sources.size === 0) state.reverseIndex.delete(target);
}

function getOrCreateRecord(state: State, key: EntityKey): EntityRecord {
  let record = state.entities.get(key);
  if (!record) {
    const { typename, id } = splitKey(key);
    record = { key, typename, id, fields: new Map(), pendingDelete: false };
    state.entities.set(key, record);
  }
  return record;
}

export function setField(
  state: State,
  key: EntityKey,
  field: string,
  value: Value,
  version: number,
): void {
  const record = getOrCreateRecord(state, key);
  const old = record.fields.get(field);
  if (old) for (const target of refsOf(old.value)) indexRemove(state, target, key, field);
  record.fields.set(field, { value, version });
  for (const target of refsOf(value)) indexAdd(state, target, key, field);
}

function rewriteFieldRefs(
  state: State,
  source: EntityKey,
  field: string,
  target: EntityKey,
  replacement: EntityKey | null,
): void {
  const record = state.entities.get(source);
  const entry = record?.fields.get(field);
  if (!entry) return;
  setField(state, source, field, rewriteRefs(entry.value, target, replacement), entry.version);
}

function removeEntityNow(state: State, key: EntityKey): void {
  const record = state.entities.get(key);
  if (!record) return;
  // drop outbound contributions
  for (const [field, entry] of record.fields) {
    for (const target of refsOf(entry.value)) indexRemove(state, target, key, field);
  }
  // null out inbound references held by surviving entities
  const inbound = state.reverseIndex.get(key);
  if (inbound) {
    for (const [source, fields] of [...inbound]) {
      if (source === key) continue;
      for (const field of fields) rewriteFieldRefs(state, source, field, key, null);
    }
  }
  state.reverseIndex.delete(key);
  state.entities.delete(key);
}

function applyDeleteEntities(state: State, keys: EntityKey[]): void {
  for (const key of keys) {
    if (!state.entities.has(key)) continue;
    if ((state.leaseCounts.get(key) ?? 0) > 0) {
      state.entities.get(key)!.pendingDelete = true;
    }
  }
  for (const key of keys) {
    if (!state.entities.has(key)) continue;
    if ((state.leaseCounts.get(key) ?? 0) === 0) removeEntityNow(state, key);
  }
}

function applyMergeIdentity(
  state: State,
  typename: string,
  fromId: string,
  toId: string,
): void {
  const fromKey = keyOf(typename, fromId);
  const toKey = keyOf(typename, toId);
  const from = state.entities.get(fromKey);
  if (!from || fromKey === toKey) return;
  getOrCreateRecord(state, toKey);
  // merge fields: higher version wins; `to` keeps its own on ties of newer data
  for (const [field, entry] of from.fields) {
    const existing = state.entities.get(toKey)!.fields.get(field);
    if (!existing || entry.version >= existing.version) {
      setField(state, toKey, field, entry.value, entry.version);
    }
  }
  // atomically repoint every inbound reference from -> to
  const inbound = state.reverseIndex.get(fromKey);
  if (inbound) {
    for (const [source, fields] of [...inbound]) {
      if (source === fromKey) continue;
      for (const field of [...fields]) rewriteFieldRefs(state, source, field, fromKey, toKey);
    }
  }
  // drop remaining outbound contributions of the absorbed record and remove it
  for (const [field, entry] of from.fields) {
    for (const target of refsOf(entry.value)) indexRemove(state, target, fromKey, field);
  }
  state.reverseIndex.delete(fromKey);
  state.entities.delete(fromKey);
  // transfer leases so live queries keep protecting the merged identity
  const count = state.leaseCounts.get(fromKey) ?? 0;
  if (count > 0) {
    state.leaseCounts.set(toKey, (state.leaseCounts.get(toKey) ?? 0) + count);
    state.leaseCounts.delete(fromKey);
    for (const keys of state.leases.values()) {
      if (keys.delete(fromKey)) keys.add(toKey);
    }
  }
}

export function applyOp(state: State, op: Op): void {
  switch (op.op) {
    case 'writeField':
      setField(state, op.key, op.field, op.value, op.version);
      break;
    case 'deleteEntities':
      applyDeleteEntities(state, op.keys);
      break;
    case 'mergeIdentity':
      applyMergeIdentity(state, op.typename, op.fromId, op.toId);
      break;
    case 'acquireLease': {
      state.leases.set(op.leaseId, new Set(op.keys));
      for (const key of op.keys) {
        state.leaseCounts.set(key, (state.leaseCounts.get(key) ?? 0) + 1);
      }
      break;
    }
    case 'releaseLease': {
      const keys = state.leases.get(op.leaseId);
      if (!keys) break;
      state.leases.delete(op.leaseId);
      for (const key of keys) {
        const count = (state.leaseCounts.get(key) ?? 0) - 1;
        if (count <= 0) state.leaseCounts.delete(key);
        else state.leaseCounts.set(key, count);
        if (count <= 0 && state.entities.get(key)?.pendingDelete) {
          removeEntityNow(state, key);
        }
      }
      break;
    }
    case 'tick':
      state.clock += 1;
      break;
  }
}

export function serializeState(state: State): unknown {
  const sortObj = <T>(entries: Iterable<[string, T]>): [string, T][] =>
    [...entries].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
  return {
    clock: state.clock,
    entities: sortObj(state.entities).map(([key, r]) => ({
      key,
      pendingDelete: r.pendingDelete,
      fields: sortObj(r.fields).map(([f, e]) => [f, e.value, e.version] as const),
    })),
    reverseIndex: sortObj(state.reverseIndex).map(([target, sources]) => [
      target,
      sortObj(sources).map(([source, fields]) => [source, [...fields].sort()] as const),
    ]),
    leaseCounts: sortObj(state.leaseCounts),
    leases: sortObj(state.leases).map(([id, keys]) => [id, [...keys].sort()] as const),
  };
}
