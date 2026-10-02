import { collectRefs, isRef } from './types.ts';
import type { EntityKey, Value } from './types.ts';

/** Committed entity record: field values plus a per-field version. */
export interface EntityRec {
  fields: Record<string, Value>;
  versions: Record<string, number>;
}

/** An optimistic overlay layer stacked on top of the committed state. */
export interface Layer {
  id: string;
  /** Version assigned when the layer began; reused when its server ack lands. */
  version: number;
  writes: Map<EntityKey, Map<string, Value>>;
  deletes: Set<EntityKey>;
}

/**
 * Store state. Treated as immutable: every commit builds a fresh State via
 * copy-on-write, so a previously handed-out Snapshot never observes a
 * transaction's intermediate or later state.
 */
export interface State {
  entities: Map<EntityKey, EntityRec>;
  /** Reverse index: target key -> (source key -> reference count). */
  reverse: Map<EntityKey, Map<EntityKey, number>>;
  /** Deleted keys -> deletion version; blocks stale resurrection. */
  tombstones: Map<EntityKey, number>;
  /** Merged identities: old key -> canonical key. */
  aliases: Map<EntityKey, EntityKey>;
  layers: Layer[];
  /** Lamport-style clock; every committed op carries an explicit version. */
  clock: number;
  /** Commit sequence number. */
  seq: number;
}

export function emptyState(): State {
  return {
    entities: new Map(),
    reverse: new Map(),
    tombstones: new Map(),
    aliases: new Map(),
    layers: [],
    clock: 0,
    seq: 0,
  };
}

/**
 * The replayable operation journal. Every state change is expressed as these
 * ops with explicit versions, so `applyOps` is deterministic and a journal
 * can be replayed into a fresh store to reproduce the exact same state.
 */
export type Op =
  | { kind: 'upsert'; key: EntityKey; fields: Record<string, Value>; version: number }
  | { kind: 'delete'; key: EntityKey; version: number }
  | { kind: 'mergeIdentity'; from: EntityKey; to: EntityKey; version: number }
  | { kind: 'layerBegin'; layerId: string; version: number }
  | { kind: 'layerWrite'; layerId: string; key: EntityKey; fields: Record<string, Value> }
  | { kind: 'layerDelete'; layerId: string; key: EntityKey }
  | { kind: 'layerCommit'; layerId: string }
  | { kind: 'layerRollback'; layerId: string };

export function resolveAlias(
  state: Pick<State, 'aliases'>,
  key: EntityKey,
): EntityKey {
  let k = key;
  while (state.aliases.has(k)) k = state.aliases.get(k)!;
  return k;
}

interface Ctx {
  entities: State['entities'];
  reverse: State['reverse'];
  tombstones: State['tombstones'];
  aliases: State['aliases'];
  layers: Layer[];
  changed: Set<EntityKey>;
  clock: number;
}

function refsOf(fields: Record<string, Value>): Map<EntityKey, number> {
  const out = new Map<EntityKey, number>();
  for (const k of Object.keys(fields)) collectRefs(fields[k], out);
  return out;
}

/** Adjust reverse[target][source] by delta, copy-on-write on the inner map. */
function addRef(ctx: Ctx, target: EntityKey, source: EntityKey, delta: number): void {
  if (delta === 0) return;
  const inner = new Map(ctx.reverse.get(target));
  const next = (inner.get(source) ?? 0) + delta;
  if (next <= 0) inner.delete(source);
  else inner.set(source, next);
  if (inner.size > 0) ctx.reverse.set(target, inner);
  else ctx.reverse.delete(target);
}

/** Replace an entity record and reconcile its outgoing reverse-index entries. */
function setEntity(ctx: Ctx, key: EntityKey, rec: EntityRec): void {
  const old = ctx.entities.get(key);
  const oldRefs = old ? refsOf(old.fields) : new Map<EntityKey, number>();
  const newRefs = refsOf(rec.fields);
  for (const [t, n] of oldRefs) {
    if (!newRefs.has(t)) addRef(ctx, t, key, -n);
  }
  for (const [t, n] of newRefs) {
    const o = oldRefs.get(t) ?? 0;
    if (o !== n) addRef(ctx, t, key, n - o);
  }
  ctx.entities.set(key, rec);
  ctx.changed.add(key);
}

/**
 * Remove references to `target` from a stored value: a direct ref becomes
 * explicit null, array entries pointing at it are dropped, inline objects are
 * cleaned recursively.
 */
function stripTarget(value: Value, target: EntityKey): { v: Value; changed: boolean } {
  if (isRef(value)) {
    return value.$ref === target ? { v: null, changed: true } : { v: value, changed: false };
  }
  if (Array.isArray(value)) {
    let changed = false;
    const out: Value[] = [];
    for (const item of value) {
      if (isRef(item) && item.$ref === target) {
        changed = true;
        continue;
      }
      const r = stripTarget(item, target);
      if (r.changed) changed = true;
      out.push(r.v);
    }
    return changed ? { v: out, changed } : { v: value, changed };
  }
  if (typeof value === 'object' && value !== null) {
    let changed = false;
    const out: Record<string, Value> = {};
    for (const k of Object.keys(value)) {
      const r = stripTarget((value as Record<string, Value>)[k], target);
      if (r.changed) changed = true;
      out[k] = r.v;
    }
    return changed ? { v: out, changed } : { v: value, changed };
  }
  return { v: value, changed: false };
}

/** Rewrite references `from` -> `to` inside a stored value (identity merge). */
function swapTarget(value: Value, from: EntityKey, to: EntityKey): { v: Value; changed: boolean } {
  if (isRef(value)) {
    return value.$ref === from ? { v: { $ref: to }, changed: true } : { v: value, changed: false };
  }
  if (Array.isArray(value)) {
    let changed = false;
    let out = value.map((item) => {
      const r = swapTarget(item, from, to);
      if (r.changed) changed = true;
      return r.v;
    });
    if (changed) {
      // The swap may have produced duplicate refs; dedupe while keeping order.
      const seen = new Set<EntityKey>();
      const deduped: Value[] = [];
      for (const item of out) {
        if (isRef(item)) {
          if (seen.has(item.$ref)) continue;
          seen.add(item.$ref);
        }
        deduped.push(item);
      }
      out = deduped;
    }
    return changed ? { v: out, changed } : { v: value, changed };
  }
  if (typeof value === 'object' && value !== null) {
    let changed = false;
    const out: Record<string, Value> = {};
    for (const k of Object.keys(value)) {
      const r = swapTarget((value as Record<string, Value>)[k], from, to);
      if (r.changed) changed = true;
      out[k] = r.v;
    }
    return changed ? { v: out, changed } : { v: value, changed };
  }
  return { v: value, changed: false };
}

function applyUpsert(ctx: Ctx, op: Extract<Op, { kind: 'upsert' }>): void {
  const key = resolveAlias(ctx, op.key);
  ctx.clock = Math.max(ctx.clock, op.version);
  const tomb = ctx.tombstones.get(key);
  if (tomb !== undefined && tomb >= op.version) return; // stale: no resurrection
  const old = ctx.entities.get(key);
  const fields: Record<string, Value> = old ? { ...old.fields } : {};
  const versions: Record<string, number> = old ? { ...old.versions } : {};
  let any = false;
  for (const f of Object.keys(op.fields)) {
    // Per-field version check: an older server response can never overwrite
    // a newer confirmed field version.
    if (op.version >= (versions[f] ?? -Infinity)) {
      fields[f] = op.fields[f];
      versions[f] = op.version;
      any = true;
    }
  }
  if (!any && old) return;
  setEntity(ctx, key, { fields, versions });
  if (tomb !== undefined && op.version > tomb) ctx.tombstones.delete(key);
}

function applyDelete(ctx: Ctx, op: Extract<Op, { kind: 'delete' }>): void {
  const key = resolveAlias(ctx, op.key);
  ctx.clock = Math.max(ctx.clock, op.version);
  ctx.tombstones.set(key, Math.max(ctx.tombstones.get(key) ?? -Infinity, op.version));
  ctx.changed.add(key);
  const rec = ctx.entities.get(key);
  if (!rec) return;
  ctx.entities.delete(key);
  // Outgoing refs: drop this entity as a source in the reverse index.
  for (const [t, n] of refsOf(rec.fields)) addRef(ctx, t, key, -n);
  // Incoming refs: every referrer is rewritten to drop the dangling ref.
  // `incoming` is a stable snapshot because inner maps are copy-on-write;
  // referrers that are themselves deleted in the same commit (cycles) are
  // skipped, which keeps reference cycles from looping forever.
  const incoming = ctx.reverse.get(key);
  ctx.reverse.delete(key);
  if (!incoming) return;
  for (const source of [...incoming.keys()]) {
    if (source === key) continue;
    const srec = ctx.entities.get(source);
    if (!srec) continue;
    const fields = { ...srec.fields };
    const versions = { ...srec.versions };
    let any = false;
    for (const f of Object.keys(fields)) {
      const r = stripTarget(fields[f], key);
      if (r.changed) {
        fields[f] = r.v;
        versions[f] = Math.max(versions[f] ?? -Infinity, op.version);
        any = true;
      }
    }
    if (any) setEntity(ctx, source, { fields, versions });
  }
}

function rewriteLayer(layer: Layer, from: EntityKey, to: EntityKey): Layer {
  const writes = new Map<EntityKey, Map<string, Value>>();
  for (const [k, fields] of layer.writes) {
    const nk = k === from ? to : k;
    const merged = writes.get(nk) ?? new Map<string, Value>();
    for (const [f, v] of fields) merged.set(f, swapTarget(v, from, to).v);
    writes.set(nk, merged);
  }
  const deletes = new Set([...layer.deletes].map((k) => (k === from ? to : k)));
  return { ...layer, writes, deletes };
}

function applyMergeIdentity(ctx: Ctx, op: Extract<Op, { kind: 'mergeIdentity' }>): void {
  const from = resolveAlias(ctx, op.from);
  const to = resolveAlias(ctx, op.to);
  ctx.clock = Math.max(ctx.clock, op.version);
  if (from === to) return;
  const fromRec = ctx.entities.get(from);
  const toRec = ctx.entities.get(to);
  // Field-level merge: the higher version wins, ties go to the incoming (from).
  const fields: Record<string, Value> = toRec ? { ...toRec.fields } : {};
  const versions: Record<string, number> = toRec ? { ...toRec.versions } : {};
  if (fromRec) {
    for (const f of Object.keys(fromRec.fields)) {
      if ((fromRec.versions[f] ?? -Infinity) >= (versions[f] ?? -Infinity)) {
        fields[f] = fromRec.fields[f];
        versions[f] = fromRec.versions[f];
      }
    }
  }
  // Self-references to the old key now point at the canonical key.
  for (const f of Object.keys(fields)) {
    const r = swapTarget(fields[f], from, to);
    if (r.changed) fields[f] = r.v;
  }
  if (fromRec) {
    ctx.entities.delete(from);
    for (const [t, n] of refsOf(fromRec.fields)) addRef(ctx, t, from, -n);
  }
  const incoming = ctx.reverse.get(from);
  ctx.reverse.delete(from);
  ctx.tombstones.set(from, Math.max(ctx.tombstones.get(from) ?? -Infinity, op.version));
  ctx.aliases.set(from, to);
  ctx.changed.add(from);
  ctx.changed.add(to);
  // Writing the canonical record recomputes its reverse-index entries. When
  // neither identity is committed yet (e.g. a purely optimistic temp id) we
  // only record the alias and rewrite the layers.
  if (fromRec || toRec) setEntity(ctx, to, { fields, versions });
  // Atomically retarget every referrer from the old key to the canonical key.
  if (incoming) {
    for (const source of [...incoming.keys()]) {
      if (source === from || source === to) continue;
      const srec = ctx.entities.get(source);
      if (!srec) continue;
      const sfields = { ...srec.fields };
      const sversions = { ...srec.versions };
      let any = false;
      for (const f of Object.keys(sfields)) {
        const r = swapTarget(sfields[f], from, to);
        if (r.changed) {
          sfields[f] = r.v;
          sversions[f] = Math.max(sversions[f] ?? -Infinity, op.version);
          any = true;
        }
      }
      if (any) setEntity(ctx, source, { fields: sfields, versions: sversions });
    }
  }
  // Pending optimistic writes move to the canonical identity too.
  ctx.layers = ctx.layers.map((l) => rewriteLayer(l, from, to));
}

function mapLayer(ctx: Ctx, layerId: string, fn: (l: Layer) => Layer): void {
  ctx.layers = ctx.layers.map((l) => (l.id === layerId ? fn(l) : l));
}

function dropLayer(ctx: Ctx, layerId: string): void {
  const layer = ctx.layers.find((l) => l.id === layerId);
  if (!layer) return;
  for (const k of layer.writes.keys()) ctx.changed.add(k);
  for (const k of layer.deletes) ctx.changed.add(k);
  ctx.layers = ctx.layers.filter((l) => l.id !== layerId);
}

/** Apply a batch of ops atomically, producing the next immutable State. */
export function applyOps(
  prev: State,
  ops: readonly Op[],
): { state: State; changed: Set<EntityKey> } {
  const ctx: Ctx = {
    entities: new Map(prev.entities),
    reverse: new Map(prev.reverse),
    tombstones: new Map(prev.tombstones),
    aliases: new Map(prev.aliases),
    layers: prev.layers,
    changed: new Set(),
    clock: prev.clock,
  };
  for (const op of ops) {
    switch (op.kind) {
      case 'upsert':
        applyUpsert(ctx, op);
        break;
      case 'delete':
        applyDelete(ctx, op);
        break;
      case 'mergeIdentity':
        applyMergeIdentity(ctx, op);
        break;
      case 'layerBegin':
        ctx.clock = Math.max(ctx.clock, op.version);
        ctx.layers = [
          ...ctx.layers,
          { id: op.layerId, version: op.version, writes: new Map(), deletes: new Set() },
        ];
        break;
      case 'layerWrite':
        mapLayer(ctx, op.layerId, (l) => {
          const writes = new Map(l.writes);
          const fields = new Map(writes.get(op.key));
          for (const [f, v] of Object.entries(op.fields)) fields.set(f, v);
          writes.set(op.key, fields);
          return { ...l, writes };
        });
        ctx.changed.add(resolveAlias(ctx, op.key));
        break;
      case 'layerDelete':
        mapLayer(ctx, op.layerId, (l) => {
          const deletes = new Set(l.deletes);
          deletes.add(op.key);
          return { ...l, deletes };
        });
        ctx.changed.add(resolveAlias(ctx, op.key));
        break;
      case 'layerCommit':
      case 'layerRollback':
        dropLayer(ctx, op.layerId);
        break;
    }
  }
  return {
    state: {
      entities: ctx.entities,
      reverse: ctx.reverse,
      tombstones: ctx.tombstones,
      aliases: ctx.aliases,
      layers: ctx.layers,
      clock: ctx.clock,
      seq: prev.seq + 1,
    },
    changed: ctx.changed,
  };
}

/**
 * Read the effective fields of an entity: committed record overlaid with the
 * optimistic layers in stack order. A layer delete hides the committed record
 * until a later layer writes the entity again.
 */
export function readEntity(
  state: State,
  key: EntityKey,
): Record<string, Value> | undefined {
  const resolved = resolveAlias(state, key);
  const base = state.entities.get(resolved);
  let fields: Record<string, Value> | undefined = base ? { ...base.fields } : undefined;
  let deleted = !base;
  for (const layer of state.layers) {
    if (layer.deletes.has(resolved)) {
      fields = {};
      deleted = true;
    }
    const w = layer.writes.get(resolved);
    if (w) {
      fields = fields ?? {};
      for (const [f, v] of w) fields[f] = v;
      deleted = false;
    }
  }
  return deleted ? undefined : fields;
}
