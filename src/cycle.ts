import type { ChainEdge, ModuleId, ModuleSpec, SymbolRef } from './types.ts';
import { edgeKey, outgoingEdges, refKey } from './common.ts';

/**
 * Detects a cycle made purely of re-export edges that is reachable from
 * `start`. Cycles passing through an import edge are allowed and yield null.
 * Deterministic: nodes and edges are visited in sorted order, so the returned
 * cycle is canonical for a given module graph.
 */
export function findReexportCycle(
  modules: ReadonlyMap<ModuleId, ModuleSpec>,
  start: SymbolRef,
): ChainEdge[] | null {
  const refs = new Map<string, SymbolRef>();
  const reEdges = new Map<string, ChainEdge[]>();
  const stack: SymbolRef[] = [start];
  refs.set(refKey(start), start);
  while (stack.length > 0) {
    const ref = stack.pop()!;
    const spec = modules.get(ref.module);
    if (!spec) continue;
    if (spec.definitions.includes(ref.name)) continue; // definitions shadow edges
    for (const edge of outgoingEdges(spec, ref)) {
      if (edge.via === 'reexport') {
        const key = refKey(edge.from);
        let arr = reEdges.get(key);
        if (!arr) {
          arr = [];
          reEdges.set(key, arr);
        }
        arr.push(edge);
      }
      const toKey = refKey(edge.to);
      if (!refs.has(toKey)) {
        refs.set(toKey, edge.to);
        stack.push(edge.to);
      }
    }
  }
  for (const edges of reEdges.values()) {
    edges.sort((a, b) => (edgeKey(a) < edgeKey(b) ? -1 : 1));
  }

  const color = new Map<string, number>(); // 1 = on stack, 2 = done
  const roots = [...refs.keys()].sort();
  for (const rootKey of roots) {
    if (color.has(rootKey)) continue;
    color.set(rootKey, 1);
    const frames: { key: string; next: number }[] = [{ key: rootKey, next: 0 }];
    const path: ChainEdge[] = []; // path[i] leads frames[i] -> frames[i+1]
    while (frames.length > 0) {
      const top = frames[frames.length - 1];
      const edges = reEdges.get(top.key) ?? [];
      if (top.next < edges.length) {
        const edge = edges[top.next++];
        const toKey = refKey(edge.to);
        const state = color.get(toKey);
        if (state === 1) {
          const idx = frames.findIndex((f) => f.key === toKey);
          return [...path.slice(idx), edge];
        }
        if (state === undefined) {
          color.set(toKey, 1);
          frames.push({ key: toKey, next: 0 });
          path.push(edge);
        }
      } else {
        color.set(top.key, 2);
        frames.pop();
        path.pop();
      }
    }
  }
  return null;
}
