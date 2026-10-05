/**
 * Reference implementation used to validate the incremental engine.
 *
 * Deliberately written as an independent, brute-force, per-query search:
 * every call re-explores the re-export graph from the queried reference site
 * with no caching, no memoization and no shared state, using different
 * algorithms from the main engine (DFS reachability + per-definition BFS
 * shortest paths + per-node self-reachability cycle checks).
 */
import type { ChainStep, ModuleSpec, Resolution, SymbolOrigin } from './types.ts';

interface Node {
  readonly module: string;
  readonly name: string;
}

const keyOf = (n: Node): string => `${n.module} ${n.name}`;

function edges(modules: ReadonlyMap<string, ModuleSpec>, node: Node): Node[] {
  const spec = modules.get(node.module);
  const out: Node[] = [];
  for (const re of spec?.reexports ?? []) {
    if ((re.as ?? re.name) === node.name) {
      out.push({ module: re.from, name: re.name });
    }
  }
  return out;
}

function isDef(modules: ReadonlyMap<string, ModuleSpec>, node: Node): boolean {
  return modules.get(node.module)?.definitions?.includes(node.name) ?? false;
}

/** Depth-first reachability from the reference site. */
function collectReachable(modules: ReadonlyMap<string, ModuleSpec>, start: Node): Node[] {
  const seen = new Set<string>([keyOf(start)]);
  const order: Node[] = [start];
  const stack: Node[] = [start];
  while (stack.length > 0) {
    const node = stack.pop()!;
    for (const next of edges(modules, node)) {
      const k = keyOf(next);
      if (!seen.has(k)) {
        seen.add(k);
        order.push(next);
        stack.push(next);
      }
    }
  }
  return order;
}

/** True if `from` can reach `target` through one or more edges. */
function reaches(modules: ReadonlyMap<string, ModuleSpec>, from: Node, target: Node): boolean {
  const seen = new Set<string>();
  const stack: Node[] = edges(modules, from);
  while (stack.length > 0) {
    const node = stack.pop()!;
    const k = keyOf(node);
    if (k === keyOf(target)) return true;
    if (seen.has(k)) continue;
    seen.add(k);
    for (const next of edges(modules, node)) stack.push(next);
  }
  return false;
}

/** BFS shortest path from `start` to `target`, parent-pointer reconstruction. */
function shortestPath(modules: ReadonlyMap<string, ModuleSpec>, start: Node, target: Node): ChainStep[] {
  const targetKey = keyOf(target);
  const parent = new Map<string, Node | null>([[keyOf(start), null]]);
  const byKey = new Map<string, Node>([[keyOf(start), start]]);
  const queue: Node[] = [start];
  while (queue.length > 0) {
    const node = queue.shift()!;
    if (keyOf(node) === targetKey) break;
    for (const next of edges(modules, node)) {
      const k = keyOf(next);
      if (!parent.has(k)) {
        parent.set(k, node);
        byKey.set(k, next);
        queue.push(next);
      }
    }
  }
  const chain: ChainStep[] = [];
  let cursor: Node | null | undefined = byKey.get(targetKey);
  while (cursor) {
    chain.unshift({ module: cursor.module, name: cursor.name });
    cursor = parent.get(keyOf(cursor));
  }
  return chain;
}

const canon = (chain: readonly ChainStep[]): string =>
  chain.map((s) => `${s.module}#${s.name}`).join(' -> ');

/**
 * Independently resolve the symbol exported as `name` by `module`,
 * searching from this single reference site.
 */
export function resolveReference(
  modules: ReadonlyMap<string, ModuleSpec>,
  module: string,
  name: string,
): Resolution {
  const start: Node = { module, name };
  const reachable = collectReachable(modules, start);
  const defs: SymbolOrigin[] = reachable.filter((n) => isDef(modules, n));

  if (defs.length === 0) {
    const cyclic = reachable.find((n) => reaches(modules, n, n));
    if (cyclic) {
      return { status: 'circular', cycle: shortestPath(modules, cyclic, cyclic) };
    }
    return { status: 'unresolved' };
  }

  const branches = defs.map((origin) => ({
    origin,
    chain: shortestPath(modules, start, origin),
  }));
  if (branches.length === 1) {
    const only = branches[0]!;
    return { status: 'resolved', origin: only.origin, chain: only.chain };
  }
  branches.sort(
    (a, b) => a.chain.length - b.chain.length || canon(a.chain).localeCompare(canon(b.chain)),
  );
  return { status: 'conflict', definitions: [branches[0]!, branches[1]!] };
}
