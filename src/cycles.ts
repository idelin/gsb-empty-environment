import type { Course, PrereqCycle } from './types.ts';

interface Edge {
  from: string;
  to: string;
  /** strict = 先修（必须更早学期）；非 strict = 同修（同学期即可） */
  strict: boolean;
  /** 是否来自课程组替代 */
  substitution: boolean;
}

function buildEdges(courses: Course[]): Edge[] {
  const edges: Edge[] = [];
  for (const c of courses) {
    for (const p of c.prereqs) {
      if (typeof p === 'string') {
        edges.push({ from: p, to: c.id, strict: true, substitution: false });
      } else {
        for (const opt of p.anyOf) {
          edges.push({ from: opt, to: c.id, strict: true, substitution: true });
        }
      }
    }
    for (const co of c.coreqs) {
      edges.push({ from: co, to: c.id, strict: false, substitution: false });
    }
  }
  return edges;
}

/** Tarjan 强连通分量 */
function stronglyConnectedComponents(nodes: string[], edges: Edge[]): string[][] {
  const adj = new Map<string, string[]>();
  for (const n of nodes) adj.set(n, []);
  for (const e of edges) {
    if (adj.has(e.from) && adj.has(e.to)) adj.get(e.from)!.push(e.to);
  }
  const index = new Map<string, number>();
  const low = new Map<string, number>();
  const onStack = new Set<string>();
  const stack: string[] = [];
  const sccs: string[][] = [];
  let counter = 0;

  const visit = (v: string): void => {
    index.set(v, counter);
    low.set(v, counter);
    counter++;
    stack.push(v);
    onStack.add(v);
    for (const w of adj.get(v)!) {
      if (!index.has(w)) {
        visit(w);
        low.set(v, Math.min(low.get(v)!, low.get(w)!));
      } else if (onStack.has(w)) {
        low.set(v, Math.min(low.get(v)!, index.get(w)!));
      }
    }
    if (low.get(v) === index.get(v)) {
      const scc: string[] = [];
      let w: string;
      do {
        w = stack.pop()!;
        onStack.delete(w);
        scc.push(w);
      } while (w !== v);
      sccs.push(scc);
    }
  };

  for (const v of nodes) if (!index.has(v)) visit(v);
  return sccs;
}

/** 在 SCC 内部找一条经过严格边的环路径（用于报告） */
function findCyclePath(scc: Set<string>, edges: Edge[]): string[] {
  const internal = edges.filter((e) => scc.has(e.from) && scc.has(e.to));
  const adj = new Map<string, Edge[]>();
  for (const e of internal) {
    if (!adj.has(e.from)) adj.set(e.from, []);
    adj.get(e.from)!.push(e);
  }
  // 从一条严格边的起点出发，DFS 找回到起点的路径
  const strictEdge = internal.find((e) => e.strict) ?? internal[0];
  const target = strictEdge.from;
  const path: string[] = [strictEdge.from, strictEdge.to];
  const visited = new Set<string>(path);
  const dfs = (v: string): boolean => {
    if (v === target) return true;
    for (const e of adj.get(v) ?? []) {
      if (e.to === target) {
        path.push(e.to);
        return true;
      }
      if (!visited.has(e.to)) {
        visited.add(e.to);
        path.push(e.to);
        if (dfs(e.to)) return true;
        path.pop();
        visited.delete(e.to);
      }
    }
    return false;
  };
  if (strictEdge.to === target) return [target, strictEdge.to];
  dfs(strictEdge.to);
  return path;
}

/**
 * 识别“无法通过同修解决的先修环”。
 *
 * 原理：同修边允许同学期修读（非严格），先修边要求严格更早学期。
 * 一个依赖环中只要含有至少一条先修边，沿环推导会得到
 * sem(c) < sem(c) 的矛盾，无论如何安排同修都无法解决。
 * 即：SCC 内部存在严格先修边 <=> 不可解环。
 */
export function findUnresolvableCycles(courses: Course[]): PrereqCycle[] {
  const edges = buildEdges(courses);
  const nodes = courses.map((c) => c.id);
  const sccs = stronglyConnectedComponents(nodes, edges);
  const cycles: PrereqCycle[] = [];

  for (const scc of sccs) {
    const set = new Set(scc);
    const internal = edges.filter((e) => set.has(e.from) && set.has(e.to));
    const isCycle = scc.length > 1 || internal.some((e) => e.from === e.to);
    if (!isCycle) continue;
    const hasStrict = internal.some((e) => e.strict);
    if (!hasStrict) continue; // 纯同修环：同学期修读即可解决
    const path = findCyclePath(set, edges);
    cycles.push({
      courses: path,
      reason:
        '环上存在严格先修边（必须更早学期完成），沿环推出学期严格小于自身的矛盾，同修无法解决',
      involvesSubstitution: internal.some((e) => e.substitution),
    });
  }
  return cycles;
}
