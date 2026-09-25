// Versioned multi-source hierarchical graph store.
//
// Guarantees:
//  - stable entity IDs across sources, deletes and restores;
//  - children may arrive before parents (orphan queue, ordered by arrival);
//  - parent corrections move whole subtrees; cycles are rejected atomically
//    (validate-then-commit: no stats or structure change on rejection);
//  - deleting a parent never cascades: children are preserved in the orphan
//    queue under the explicit policy 'preserve-children-as-orphans';
//  - duplicate event IDs (across batches) are applied at most once;
//  - subtree aggregates are maintained incrementally along ancestor chains
//    only; `lastTouched` exposes exactly which nodes were updated;
//  - per-batch snapshots allow restoring older source views.

import { GraphError, PARENT_FIELD } from './core.ts';
import type {
  AppliedError,
  Batch,
  DeleteEvent,
  EntityNode,
  Event,
  JsonValue,
  SourceConfig,
  UpsertEvent,
} from './core.ts';
import { fieldsHaveConflict, newField, resolveField } from './fields.ts';

export const DELETE_POLICY = 'preserve-children-as-orphans' as const;

export interface StoreOptions {
  sources: SourceConfig[];
  onParentDelete: typeof DELETE_POLICY;
}

interface Snapshot {
  nodes: Map<string, EntityNode>;
  orphans: string[];
  tombstones: Map<string, number>;
  appliedEvents: string[];
}

function freshNode(id: string): EntityNode {
  return {
    id,
    presence: new Map(),
    fields: new Map(),
    parent: null,
    children: new Set(),
    subtreeSize: 1,
    subtreeConflicts: 0,
    conflict: false,
  };
}

export class Store {
  readonly priorities = new Map<string, number>();
  readonly childPolicy: string = DELETE_POLICY;
  nodes = new Map<string, EntityNode>();
  orphans: string[] = []; // ordered queue of ids whose effective parent is missing
  tombstones = new Map<string, number>(); // `${entity} ${source}` -> delete version
  appliedEvents = new Set<string>();
  errors: AppliedError[] = [];
  batches: Batch[] = [];
  snapshots = new Map<string, Snapshot>();
  lastTouched = new Set<string>(); // nodes updated by the most recent event
  counters = { applied: 0, duplicates: 0, rejected: 0 };

  constructor(options: StoreOptions) {
    if (options.onParentDelete !== DELETE_POLICY) {
      throw new GraphError(
        'INVALID_POLICY',
        `unsupported onParentDelete policy: ${String(options.onParentDelete)}`,
        '$.options.onParentDelete',
      );
    }
    for (const s of options.sources) {
      if (this.priorities.has(s.id)) {
        throw new GraphError('DUPLICATE_SOURCE', `duplicate source id: ${s.id}`, '$.options.sources');
      }
      this.priorities.set(s.id, s.priority);
    }
  }

  applyBatch(batch: Batch): void {
    this.batches.push(batch);
    for (const ev of batch.events) {
      try {
        this.applyEvent(ev);
      } catch (err) {
        if (err instanceof GraphError) {
          this.errors.push({
            code: err.code,
            message: err.message,
            path: err.path,
            eventId: ev.eventId,
            batch: batch.id,
          });
          this.counters.rejected++;
        } else {
          throw err;
        }
      }
    }
    this.snapshots.set(batch.id, this.takeSnapshot());
  }

  restore(batchId: string): boolean {
    const snap = this.snapshots.get(batchId);
    if (!snap) return false;
    const s = structuredClone(snap);
    this.nodes = s.nodes;
    this.orphans = s.orphans;
    this.tombstones = s.tombstones;
    this.appliedEvents = new Set(s.appliedEvents);
    return true;
  }

  lineage(entityId: string): Record<string, unknown> | null {
    const node = this.nodes.get(entityId);
    if (!node) return null;
    const out: Record<string, unknown> = {};
    for (const [name, f] of node.fields) {
      out[name] = {
        effective: f.effective
          ? { source: f.effective.source, version: f.effective.version, value: f.effective.value, path: f.effective.path }
          : null,
        pending: f.pending
          ? f.pending.map((c) => ({ source: c.source, version: c.version, value: c.value, path: c.path }))
          : null,
        contributions: [...f.contributions.values()].map((c) => ({
          source: c.source,
          version: c.version,
          value: c.value,
          path: c.path,
        })),
      };
    }
    return out;
  }

  private takeSnapshot(): Snapshot {
    return structuredClone({
      nodes: this.nodes,
      orphans: this.orphans,
      tombstones: this.tombstones,
      appliedEvents: [...this.appliedEvents],
    });
  }

  private applyEvent(ev: Event): void {
    this.lastTouched = new Set();
    if (this.appliedEvents.has(ev.eventId)) {
      this.counters.duplicates++;
      return;
    }
    if (ev.kind === 'upsert') this.applyUpsert(ev);
    else this.applyDelete(ev);
    this.appliedEvents.add(ev.eventId);
    this.counters.applied++;
  }

  private applyUpsert(ev: UpsertEvent): void {
    const tomb = this.tombstones.get(`${ev.entity} ${ev.source}`);
    if (tomb !== undefined && ev.version <= tomb) return; // stale vs tombstone
    const prev = this.nodes.get(ev.entity);
    const work = prev ? structuredClone(prev) : freshNode(ev.entity);
    const pres = work.presence.get(ev.source);
    if (!pres || ev.version > pres.version) {
      work.presence.set(ev.source, { version: ev.version, path: ev.path });
    }
    if (ev.parent !== undefined) {
      if (ev.parent !== null && typeof ev.parent !== 'string') {
        throw new GraphError('INVALID_PARENT', 'parent must be a string id or null', `${ev.path}.parent`);
      }
      this.putContribution(work, PARENT_FIELD, ev.source, ev.version, ev.parent, `${ev.path}.parent`);
    }
    if (ev.fields) {
      for (const [name, value] of Object.entries(ev.fields)) {
        this.putContribution(work, name, ev.source, ev.version, value, `${ev.path}.fields.${name}`);
      }
    }
    for (const f of work.fields.values()) resolveField(f, this.priorities);
    work.parent = this.resolveParent(work);
    this.commit(work, prev, ev);
  }

  private applyDelete(ev: DeleteEvent): void {
    const tombKey = `${ev.entity} ${ev.source}`;
    const tomb = this.tombstones.get(tombKey);
    if (tomb !== undefined && ev.version <= tomb) return;
    const prev = this.nodes.get(ev.entity);
    if (!prev) {
      this.tombstones.set(tombKey, ev.version);
      return;
    }
    const pres = prev.presence.get(ev.source);
    if (!pres || ev.version < pres.version) return; // unknown source or stale delete
    const work = structuredClone(prev);
    work.presence.delete(ev.source);
    for (const [name, f] of [...work.fields]) {
      f.contributions.delete(ev.source);
      if (f.contributions.size === 0) work.fields.delete(name);
    }
    for (const f of work.fields.values()) resolveField(f, this.priorities);
    if (work.presence.size === 0) {
      this.removeEntity(prev);
    } else {
      work.parent = this.resolveParent(work);
      this.commit(work, prev, ev);
    }
    this.tombstones.set(tombKey, ev.version);
  }

  private putContribution(
    node: EntityNode,
    field: string,
    source: string,
    version: number,
    value: JsonValue,
    path: string,
  ): void {
    let f = node.fields.get(field);
    if (!f) {
      f = newField();
      node.fields.set(field, f);
    }
    const existing = f.contributions.get(source);
    if (existing && existing.version >= version) return; // stale contribution
    f.contributions.set(source, { source, version, value, path });
  }

  // Effective parent: pending parent conflicts keep the current parent
  // (documented "hold" policy — never pick one side arbitrarily).
  private resolveParent(work: EntityNode): string | null {
    const pf = work.fields.get(PARENT_FIELD);
    if (!pf || pf.contributions.size === 0) return null;
    if (pf.pending || !pf.effective) return work.parent;
    return pf.effective.value as string | null;
  }

  private commit(work: EntityNode, prev: EntityNode | undefined, ev: Event): void {
    const parentChanged = (prev?.parent ?? null) !== work.parent;
    if (parentChanged && work.parent !== null) {
      this.assertNoCycle(work.id, work.parent, `${ev.path}.parent`);
    }
    const selfDelta = (fieldsHaveConflict(work.fields) ? 1 : 0) - (prev?.conflict ? 1 : 0);
    work.conflict = fieldsHaveConflict(work.fields);
    work.subtreeConflicts = (prev?.subtreeConflicts ?? 0) + selfDelta;

    if (prev && parentChanged) this.detach(prev);
    if ((parentChanged || !prev) && work.parent !== null) {
      const parent = this.nodes.get(work.parent);
      if (parent) {
        parent.children.add(work.id);
        this.adjustChain(work.parent, work.subtreeSize, work.subtreeConflicts);
      } else if (!this.orphans.includes(work.id)) {
        this.orphans.push(work.id);
      }
    }
    this.nodes.set(work.id, work);
    this.lastTouched.add(work.id);
    if (prev && !parentChanged && selfDelta !== 0 && work.parent !== null) {
      this.adjustChain(work.parent, 0, selfDelta);
    }
    this.attachOrphansTo(work);
  }

  private detach(node: EntityNode): void {
    if (node.parent === null) {
      this.removeOrphan(node.id);
      return;
    }
    const parent = this.nodes.get(node.parent);
    if (parent) {
      parent.children.delete(node.id);
      this.adjustChain(node.parent, -node.subtreeSize, -node.subtreeConflicts);
    } else {
      this.removeOrphan(node.id);
    }
  }

  // Explicit policy 'preserve-children-as-orphans': children keep their
  // identity and parent claim, and wait in the orphan queue. No cascade.
  private removeEntity(node: EntityNode): void {
    this.detach(node);
    for (const childId of [...node.children]) {
      if (!this.orphans.includes(childId)) this.orphans.push(childId);
      this.lastTouched.add(childId);
    }
    node.children.clear();
    this.nodes.delete(node.id);
    this.lastTouched.add(node.id);
  }

  private attachOrphansTo(node: EntityNode): void {
    for (const orphanId of [...this.orphans]) {
      const orphan = this.nodes.get(orphanId);
      if (orphan && orphan.parent === node.id) {
        this.removeOrphan(orphanId);
        node.children.add(orphanId);
        this.adjustChain(node.id, orphan.subtreeSize, orphan.subtreeConflicts);
      }
    }
  }

  private adjustChain(startId: string, deltaSize: number, deltaConflicts: number): void {
    const seen = new Set<string>();
    let cur: string | null = startId;
    while (cur !== null && !seen.has(cur)) {
      seen.add(cur);
      const node = this.nodes.get(cur);
      if (!node) break;
      node.subtreeSize += deltaSize;
      node.subtreeConflicts += deltaConflicts;
      this.lastTouched.add(cur);
      cur = node.parent;
    }
  }

  private assertNoCycle(entityId: string, newParent: string, path: string): void {
    if (newParent === entityId) {
      throw new GraphError('CYCLE', `entity ${entityId} cannot be its own parent`, path);
    }
    const seen = new Set<string>();
    let cur: string | null = newParent;
    while (cur !== null && !seen.has(cur)) {
      if (cur === entityId) {
        throw new GraphError(
          'CYCLE',
          `moving ${entityId} under ${newParent} would create a cycle`,
          path,
        );
      }
      seen.add(cur);
      cur = this.nodes.get(cur)?.parent ?? null;
    }
  }

  private removeOrphan(id: string): void {
    const index = this.orphans.indexOf(id);
    if (index >= 0) this.orphans.splice(index, 1);
  }
}
