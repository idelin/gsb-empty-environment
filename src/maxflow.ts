/**
 * Dinic 最大流算法，用于求解 s-t 最小割。
 * 仅使用可被 Node 类型擦除（type stripping）支持的 TypeScript 语法。
 */

interface Edge {
  to: number;
  cap: number;
  rev: number;
}

const EPS = 1e-12;

export class Dinic {
  readonly nodeCount: number;
  private readonly graph: Edge[][];
  private readonly level: number[];
  private readonly iter: number[];

  constructor(nodeCount: number) {
    this.nodeCount = nodeCount;
    this.graph = Array.from({ length: nodeCount }, () => []);
    this.level = new Array<number>(nodeCount).fill(0);
    this.iter = new Array<number>(nodeCount).fill(0);
  }

  addEdge(from: number, to: number, cap: number): void {
    this.graph[from].push({ to, cap, rev: this.graph[to].length });
    this.graph[to].push({ to: from, cap: 0, rev: this.graph[from].length - 1 });
  }

  private bfs(s: number, t: number): boolean {
    this.level.fill(-1);
    const queue: number[] = [s];
    this.level[s] = 0;
    for (let head = 0; head < queue.length; head++) {
      const v = queue[head];
      for (const e of this.graph[v]) {
        if (e.cap > EPS && this.level[e.to] < 0) {
          this.level[e.to] = this.level[v] + 1;
          queue.push(e.to);
        }
      }
    }
    return this.level[t] >= 0;
  }

  private dfs(v: number, t: number, f: number): number {
    if (v === t) return f;
    const edges = this.graph[v];
    for (; this.iter[v] < edges.length; this.iter[v]++) {
      const e = edges[this.iter[v]];
      if (e.cap > EPS && this.level[v] < this.level[e.to]) {
        const d = this.dfs(e.to, t, Math.min(f, e.cap));
        if (d > EPS) {
          e.cap -= d;
          this.graph[e.to][e.rev].cap += d;
          return d;
        }
      }
    }
    return 0;
  }

  maxFlow(s: number, t: number): number {
    let flow = 0;
    while (this.bfs(s, t)) {
      this.iter.fill(0);
      let f: number;
      while ((f = this.dfs(s, t, Number.POSITIVE_INFINITY)) > EPS) {
        flow += f;
      }
    }
    return flow;
  }

  /** 最大流计算完成后，返回残余网络中从 s 可达的节点集合（最小割的源侧）。 */
  reachableFrom(s: number): boolean[] {
    const seen = new Array<boolean>(this.nodeCount).fill(false);
    const stack: number[] = [s];
    seen[s] = true;
    while (stack.length > 0) {
      const v = stack.pop() as number;
      for (const e of this.graph[v]) {
        if (e.cap > EPS && !seen[e.to]) {
          seen[e.to] = true;
          stack.push(e.to);
        }
      }
    }
    return seen;
  }
}
