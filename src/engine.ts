import type {
  ChainEdge,
  ModuleId,
  ModuleSpec,
  Resolution,
  SourceChain,
  SymbolRef,
} from './types.ts';
import { compareChains, edgesKey, outgoingEdges, refKey } from './common.ts';
import { findReexportCycle } from './cycle.ts';

interface CacheEntry {
  result: Resolution;
  /** Modules whose specs (or absence) were observed while resolving. */
  deps: Set<ModuleId>;
}

interface VersionState {
  modules: ReadonlyMap<ModuleId, ModuleSpec>;
  cache: Map<string, CacheEntry>;
}

/**
 * Resolves (start.module, start.name) against a frozen module map.
 *
 * Layered BFS keeps, per reached symbol, the lexicographically smallest
 * shortest hop sequence, so results are canonical and independent of spec
 * field ordering. A local definition shadows any import/re-export of the same
 * name. The search stops after completing the layer in which a second
 * distinct definition target is found; anything unreached is strictly farther
 * and cannot displace the two shortest source chains.
 */
export function computeResolution(
  modules: ReadonlyMap<ModuleId, ModuleSpec>,
  start: SymbolRef,
): { result: Resolution; deps: Set<ModuleId> } {
  const deps = new Set<ModuleId>();
  const visited = new Set<string>();
  const targets = new Map<string, SourceChain>();
  let layer: { ref: SymbolRef; edges: ChainEdge[] }[] = [{ ref: start, edges: [] }];

  while (layer.length > 0) {
    const next = new Map<string, { ref: SymbolRef; edges: ChainEdge[] }>();
    for (const node of layer) {
      const key = refKey(node.ref);
      if (visited.has(key)) continue;
      visited.add(key);
      deps.add(node.ref.module);
      const spec = modules.get(node.ref.module);
      if (!spec) continue;
      if (spec.definitions.includes(node.ref.name)) {
        targets.set(key, { edges: node.edges, target: node.ref });
        continue;
      }
      for (const edge of outgoingEdges(spec, node.ref)) {
        const toKey = refKey(edge.to);
        if (visited.has(toKey)) continue;
        const candidate = { ref: edge.to, edges: [...node.edges, edge] };
        const prev = next.get(toKey);
        if (!prev || edgesKey(candidate.edges) < edgesKey(prev.edges)) {
          next.set(toKey, candidate);
        }
      }
    }
    if (targets.size >= 2) break;
    layer = [...next.values()];
  }

  if (targets.size === 0) {
    const cycle = findReexportCycle(modules, start);
    const result: Resolution = cycle
      ? { kind: 'cyclic-reexport', cycle }
      : { kind: 'undefined' };
    return { result, deps };
  }
  const chains = [...targets.values()].sort(compareChains);
  if (chains.length === 1) {
    return {
      result: { kind: 'defined', target: chains[0].target, chain: chains[0] },
      deps,
    };
  }
  return { result: { kind: 'conflict', chains: [chains[0], chains[1]] }, deps };
}

/**
 * Incremental module graph with queryable snapshots.
 *
 * Every update produces a new version. Resolution results are memoized per
 * version together with the set of modules they observed; updating module M
 * drops exactly the cached entries whose dependency set contains M, so only
 * affected symbols are recomputed. Older versions keep their own module map
 * (structurally shared) and cache, and remain fully queryable.
 */
export class Engine {
  private states: VersionState[] = [{ modules: new Map(), cache: new Map() }];

  get version(): number {
    return this.states.length - 1;
  }

  modulesAt(version: number): ReadonlyMap<ModuleId, ModuleSpec> {
    const state = this.states[version];
    if (!state) throw new RangeError(`unknown version ${version}`);
    return state.modules;
  }

  /** Replaces (or deletes, when spec is null) one module; returns the new version. */
  update(module: ModuleId, spec: ModuleSpec | null): { version: number; invalidated: string[] } {
    const prev = this.states[this.states.length - 1];
    const modules = new Map(prev.modules);
    if (spec === null) modules.delete(module);
    else modules.set(module, spec);
    const cache = new Map<string, CacheEntry>();
    const invalidated: string[] = [];
    for (const [key, entry] of prev.cache) {
      if (entry.deps.has(module)) invalidated.push(key);
      else cache.set(key, entry);
    }
    this.states.push({ modules, cache });
    return { version: this.states.length - 1, invalidated };
  }

  resolve(module: ModuleId, name: string, version: number = this.version): Resolution {
    const state = this.states[version];
    if (!state) throw new RangeError(`unknown version ${version}`);
    const key = refKey({ module, name });
    const hit = state.cache.get(key);
    if (hit) return hit.result;
    const { result, deps } = computeResolution(state.modules, { module, name });
    state.cache.set(key, { result, deps });
    return result;
  }
}
