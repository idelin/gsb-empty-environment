/**
 * Core resolution engine (main implementation).
 *
 * Resolution of "the symbol exported as N by module M" is a reachability
 * problem over the re-export graph whose nodes are (module, name) pairs:
 *
 *  - (M, N) is a definition terminal if M declares N in `definitions`.
 *  - (M, N) has an edge to (T, name) for every `export { name as N } from 'T'`
 *    (or `export { name } from 'T'`) in M.
 *
 * The result is the set of definition terminals reachable from (M, N):
 *  - exactly one  -> resolved (with the shortest provenance chain)
 *  - two or more  -> conflict, reported with the two shortest source chains
 *  - none, and the reachable subgraph contains a cycle -> circular re-export
 *  - none, acyclic -> unresolved
 *
 * Circular imports are inherently allowed: imports are leaf references and
 * never feed back into export resolution.
 */
import type { ChainStep, ModuleSpec, Resolution, SymbolOrigin } from './types.ts';

export interface CacheEntry {
  readonly result: Resolution;
  /** Ids of every module whose spec was read while computing `result`. */
  readonly deps: ReadonlySet<string>;
}

export interface ComputedResolution {
  readonly result: Resolution;
  readonly deps: ReadonlySet<string>;
}

const SEP = ' ';

function nodeKey(module: string, name: string): string {
  return `${module}${SEP}${name}`;
}

function parseNodeKey(key: string): ChainStep {
  const idx = key.lastIndexOf(SEP);
  return { module: key.slice(0, idx), name: key.slice(idx + 1) };
}

export function cacheKey(module: string, name: string): string {
  return nodeKey(module, name);
}

export function canonicalChain(chain: readonly ChainStep[]): string {
  return chain.map((s) => `${s.module}#${s.name}`).join(' -> ');
}

export function formatChain(chain: readonly ChainStep[]): string {
  return canonicalChain(chain);
}

interface ReexportEdge {
  readonly targetModule: string;
  readonly targetName: string;
}

function reexportEdgesOf(spec: ModuleSpec | undefined, exportedName: string): ReexportEdge[] {
  const edges: ReexportEdge[] = [];
  for (const re of spec?.reexports ?? []) {
    if ((re.as ?? re.name) === exportedName) {
      edges.push({ targetModule: re.from, targetName: re.name });
    }
  }
  return edges;
}

function isDefinition(spec: ModuleSpec | undefined, name: string): boolean {
  return spec?.definitions?.includes(name) ?? false;
}

function compareBranches(
  a: { readonly chain: readonly ChainStep[] },
  b: { readonly chain: readonly ChainStep[] },
): number {
  return a.chain.length - b.chain.length || canonicalChain(a.chain).localeCompare(canonicalChain(b.chain));
}

/**
 * Computes the resolution of (startModule, startName) from scratch and records
 * every module id consulted, so callers can invalidate cached results precisely.
 */
export function computeResolution(
  modules: ReadonlyMap<string, ModuleSpec>,
  startModule: string,
  startName: string,
): ComputedResolution {
  const deps = new Set<string>([startModule]);
  const visited = new Set<string>([nodeKey(startModule, startName)]);
  const found = new Map<string, { origin: SymbolOrigin; chain: ChainStep[] }>();

  const start: ChainStep = { module: startModule, name: startName };
  const queue: { step: ChainStep; chain: ChainStep[] }[] = [{ step: start, chain: [start] }];

  while (queue.length > 0) {
    const { step, chain } = queue.shift()!;
    const spec = modules.get(step.module);
    if (isDefinition(spec, step.name)) {
      const key = nodeKey(step.module, step.name);
      if (!found.has(key)) {
        found.set(key, { origin: { module: step.module, name: step.name }, chain });
      }
    }
    for (const edge of reexportEdgesOf(spec, step.name)) {
      deps.add(edge.targetModule);
      const next: ChainStep = { module: edge.targetModule, name: edge.targetName };
      const key = nodeKey(next.module, next.name);
      if (!visited.has(key)) {
        visited.add(key);
        queue.push({ step: next, chain: [...chain, next] });
      }
    }
  }

  if (found.size === 1) {
    const only = [...found.values()][0]!;
    return { result: { status: 'resolved', origin: only.origin, chain: only.chain }, deps };
  }
  if (found.size >= 2) {
    const ordered = [...found.values()].sort(compareBranches);
    const first = ordered[0]!;
    const second = ordered[1]!;
    return {
      result: {
        status: 'conflict',
        definitions: [
          { origin: first.origin, chain: first.chain },
          { origin: second.origin, chain: second.chain },
        ],
      },
      deps,
    };
  }
  const cycle = findCycle(modules, visited);
  if (cycle !== null) {
    return { result: { status: 'circular', cycle }, deps };
  }
  return { result: { status: 'unresolved' }, deps };
}

/**
 * Three-color iterative DFS over the visited subgraph. Returns one concrete
 * cycle (as chain steps, first node repeated at the end) or null.
 */
function findCycle(modules: ReadonlyMap<string, ModuleSpec>, visited: ReadonlySet<string>): ChainStep[] | null {
  const IN_STACK = 1;
  const DONE = 2;
  const color = new Map<string, number>();
  const stack: ChainStep[] = [];

  const sortedEdges = (key: string): ChainStep[] => {
    const { module, name } = parseNodeKey(key);
    return reexportEdgesOf(modules.get(module), name)
      .map((e) => ({ module: e.targetModule, name: e.targetName }))
      .filter((s) => visited.has(nodeKey(s.module, s.name)))
      .sort((a, b) => nodeKey(a.module, a.name).localeCompare(nodeKey(b.module, b.name)));
  };

  for (const startKey of [...visited].sort()) {
    if (color.has(startKey)) continue;
    color.set(startKey, IN_STACK);
    stack.push(parseNodeKey(startKey));
    const frames: { key: string; edges: ChainStep[]; nextIndex: number }[] = [
      { key: startKey, edges: sortedEdges(startKey), nextIndex: 0 },
    ];
    while (frames.length > 0) {
      const frame = frames[frames.length - 1]!;
      if (frame.nextIndex < frame.edges.length) {
        const target = frame.edges[frame.nextIndex++]!;
        const targetKey = nodeKey(target.module, target.name);
        const targetColor = color.get(targetKey);
        if (targetColor === IN_STACK) {
          const idx = stack.findIndex((s) => nodeKey(s.module, s.name) === targetKey);
          return [...stack.slice(idx), target];
        }
        if (targetColor === undefined) {
          color.set(targetKey, IN_STACK);
          stack.push(target);
          frames.push({ key: targetKey, edges: sortedEdges(targetKey), nextIndex: 0 });
        }
      } else {
        color.set(frame.key, DONE);
        stack.pop();
        frames.pop();
      }
    }
  }
  return null;
}
