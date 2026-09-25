// Full-rebuild cross-check and aggregate verification helpers.

import type { Batch, SourceConfig } from './core.ts';
import { DELETE_POLICY, Store } from './store.ts';

// Replays every recorded batch into a fresh store. Duplicate/rejected events are
// reprocessed identically, so the result must match the incremental store.
export function rebuild(sources: SourceConfig[], batches: Batch[]): Store {
  const store = new Store({ sources, onParentDelete: DELETE_POLICY });
  for (const batch of batches) store.applyBatch(batch);
  return store;
}

// Deterministic, JSON-friendly projection of everything that matters.
export function canonical(store: Store): unknown {
  const nodes = [...store.nodes.values()]
    .sort((a, b) => (a.id < b.id ? -1 : 1))
    .map((n) => ({
      id: n.id,
      parent: n.parent,
      subtreeSize: n.subtreeSize,
      subtreeConflicts: n.subtreeConflicts,
      conflict: n.conflict,
      presence: [...n.presence.entries()]
        .sort(([a], [b]) => (a < b ? -1 : 1))
        .map(([source, p]) => ({ source, version: p.version, path: p.path })),
      children: [...n.children].sort(),
      fields: [...n.fields.entries()]
        .sort(([a], [b]) => (a < b ? -1 : 1))
        .map(([name, f]) => ({
          name,
          effective: f.effective
            ? {
                source: f.effective.source,
                version: f.effective.version,
                value: f.effective.value,
                path: f.effective.path,
              }
            : null,
          pending: f.pending
            ? f.pending.map((c) => ({ source: c.source, version: c.version, value: c.value }))
            : null,
          contributions: [...f.contributions.values()]
            .map((c) => ({ source: c.source, version: c.version, value: c.value, path: c.path }))
            .sort((a, b) => (a.source < b.source ? -1 : 1)),
        })),
    }));
  return {
    nodes,
    orphans: [...store.orphans],
    tombstones: [...store.tombstones.entries()]
      .sort(([a], [b]) => (a < b ? -1 : 1))
      .map(([key, version]) => ({ key, version })),
    errors: store.errors.map((e) => ({
      code: e.code,
      path: e.path,
      eventId: e.eventId,
      batch: e.batch,
    })),
    counters: { ...store.counters },
  };
}

// Independent bottom-up recomputation of the subtree aggregates.
export function recomputeStats(store: Store): Map<string, { size: number; conflicts: number }> {
  const result = new Map<string, { size: number; conflicts: number }>();
  const visiting = new Set<string>();
  const dfs = (id: string): { size: number; conflicts: number } => {
    const cached = result.get(id);
    if (cached) return cached;
    if (visiting.has(id)) throw new Error(`cycle detected during recompute at ${id}`);
    const node = store.nodes.get(id);
    if (!node) return { size: 0, conflicts: 0 };
    visiting.add(id);
    let size = 1;
    let conflicts = node.conflict ? 1 : 0;
    for (const childId of node.children) {
      const child = dfs(childId);
      size += child.size;
      conflicts += child.conflicts;
    }
    visiting.delete(id);
    const stats = { size, conflicts };
    result.set(id, stats);
    return stats;
  };
  for (const id of store.nodes.keys()) dfs(id);
  return result;
}

// Returns the ids whose stored aggregates disagree with recomputation.
export function statsMismatches(store: Store): string[] {
  const expected = recomputeStats(store);
  const mismatches: string[] = [];
  for (const [id, node] of store.nodes) {
    const want = expected.get(id)!;
    if (node.subtreeSize !== want.size || node.subtreeConflicts !== want.conflicts) {
      mismatches.push(id);
    }
  }
  return mismatches;
}
