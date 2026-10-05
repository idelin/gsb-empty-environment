import type { TreeNode } from '../src/types.ts';

/** Compact tree builder: N('a', {value:'1', id:'x'}, [children...]) */
export function N(
  type: string,
  opts: { value?: string; id?: string } = {},
  children: TreeNode[] = [],
): TreeNode {
  const n: TreeNode = { type, children };
  if (opts.value !== undefined) n.value = opts.value;
  if (opts.id !== undefined) n.id = opts.id;
  return n;
}

/** Deterministic PRNG (mulberry32). */
export function prng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface RandomTreeOpts {
  maxDepth: number;
  maxChildren: number;
  types: string[];
  withValues: boolean;
  idPool: string[]; // empty = no ids
  maxNodes: number;
}

export function randomTree(rand: () => number, opts: RandomTreeOpts): TreeNode {
  let nodes = 0;
  const gen = (depth: number): TreeNode => {
    nodes++;
    const type = opts.types[Math.floor(rand() * opts.types.length)];
    const n: TreeNode = { type, children: [] };
    if (opts.withValues && rand() < 0.5) n.value = String(Math.floor(rand() * 3));
    if (opts.idPool.length > 0 && rand() < 0.35) {
      n.id = opts.idPool[Math.floor(rand() * opts.idPool.length)];
    }
    if (depth < opts.maxDepth) {
      const k = Math.floor(rand() * (opts.maxChildren + 1));
      for (let i = 0; i < k && nodes < opts.maxNodes; i++) {
        n.children.push(gen(depth + 1));
      }
    }
    return n;
  };
  return gen(0);
}

/** Enumerate all ordered trees with up to `maxNodes` nodes over `types`. */
export function allTrees(maxNodes: number, types: string[]): TreeNode[] {
  const out: TreeNode[] = [];
  const bySize: TreeNode[][] = [[]];
  for (let size = 1; size <= maxNodes; size++) {
    const cur: TreeNode[] = [];
    for (const type of types) {
      // Distribute size-1 nodes among k children.
      const build = (remaining: number, acc: TreeNode[]): void => {
        if (remaining === 0) {
          cur.push({ type, children: acc });
          return;
        }
        for (let s = 1; s <= remaining; s++) {
          for (const child of bySize[s]) {
            build(remaining - s, [...acc, child]);
          }
        }
      };
      build(size - 1, []);
    }
    bySize.push(cur);
    out.push(...cur);
  }
  return out;
}
