import type {
  ChainEdge,
  ModuleId,
  ModuleSpec,
  Resolution,
  SourceChain,
  SymbolRef,
} from './types.ts';
import { compareChains, refKey } from './common.ts';
import { findReexportCycle } from './cycle.ts';

/**
 * Reference resolver used to validate the incremental engine. For every query
 * it performs an independent exhaustive depth-first search from the reference
 * site, enumerating all simple paths to definitions with no caching and no
 * shared traversal code with the engine.
 */
function edgesFrom(spec: ModuleSpec, ref: SymbolRef): ChainEdge[] {
  const out: ChainEdge[] = [];
  const seen = new Set<string>();
  for (const imp of spec.imports) {
    if (imp.local !== ref.name) continue;
    const key = `i\0${imp.from}\0${imp.imported}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ from: ref, via: 'import', to: { module: imp.from, name: imp.imported } });
  }
  for (const re of spec.reexports) {
    if (re.exported !== ref.name) continue;
    const key = `r\0${re.from}\0${re.imported}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ from: ref, via: 'reexport', to: { module: re.from, name: re.imported } });
  }
  return out;
}

export function resolveReference(
  modules: ReadonlyMap<ModuleId, ModuleSpec>,
  start: SymbolRef,
): Resolution {
  const chains: SourceChain[] = [];
  const onPath = new Set<string>([refKey(start)]);
  const walk = (ref: SymbolRef, edges: ChainEdge[]): void => {
    const spec = modules.get(ref.module);
    if (!spec) return;
    if (spec.definitions.includes(ref.name)) {
      chains.push({ edges, target: ref });
      return; // a local definition shadows any import/re-export of the name
    }
    for (const edge of edgesFrom(spec, ref)) {
      const key = refKey(edge.to);
      if (onPath.has(key)) continue;
      onPath.add(key);
      walk(edge.to, [...edges, edge]);
      onPath.delete(key);
    }
  };
  walk(start, []);

  if (chains.length === 0) {
    const cycle = findReexportCycle(modules, start);
    return cycle ? { kind: 'cyclic-reexport', cycle } : { kind: 'undefined' };
  }
  const bestByTarget = new Map<string, SourceChain>();
  for (const chain of chains) {
    const key = refKey(chain.target);
    const prev = bestByTarget.get(key);
    if (!prev || compareChains(chain, prev) < 0) bestByTarget.set(key, chain);
  }
  const ordered = [...bestByTarget.values()].sort(compareChains);
  if (ordered.length === 1) {
    return { kind: 'defined', target: ordered[0].target, chain: ordered[0] };
  }
  return { kind: 'conflict', chains: [ordered[0], ordered[1]] };
}
