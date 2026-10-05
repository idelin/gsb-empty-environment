/**
 * 整数容量网络上的最小费用最大流。
 *
 * 采用连续最短路（SSP）算法，每轮用 SPFA 在残量网络中找最短路并尽量多地增广。
 * 所有容量与费用均为整数，因此流量与费用也都是整数。
 * 支持负费用边（例如负电价），初始残量网络无负环时算法正确。
 */

export interface FlowEdge {
  to: number;
  /** 反向边在邻接表中的下标。 */
  rev: number;
  /** 剩余容量。 */
  cap: number;
  /** 初始容量（用于读取最终流量：flow = cap0 - cap）。 */
  cap0: number;
  cost: number;
}

export interface FlowSolution {
  flow: number;
  cost: number;
}

export class MinCostFlow {
  private readonly graph: FlowEdge[][];

  constructor(nodeCount: number) {
    this.graph = [];
    for (let i = 0; i < nodeCount; i++) {
      this.graph.push([]);
    }
  }

  /** 添加一条边，返回前向边引用（之后可通过 cap0 - cap 读取流量）。 */
  addEdge(from: number, to: number, cap: number, cost: number): FlowEdge {
    const forward: FlowEdge = { to, rev: this.graph[to].length, cap, cap0: cap, cost };
    const backward: FlowEdge = { to: from, rev: this.graph[from].length, cap: 0, cap0: 0, cost: -cost };
    this.graph[from].push(forward);
    this.graph[to].push(backward);
    return forward;
  }

  /** 求 source -> sink 的最小费用最大流（流量不超过 flowLimit）。 */
  solve(source: number, sink: number, flowLimit: number = Number.MAX_SAFE_INTEGER): FlowSolution {
    let flow = 0;
    let cost = 0;
    const n = this.graph.length;
    const dist = new Array<number>(n);
    const prevNode = new Array<number>(n);
    const prevEdge = new Array<number>(n);
    const inQueue = new Array<boolean>(n);

    while (flow < flowLimit) {
      dist.fill(Infinity);
      prevNode.fill(-1);
      prevEdge.fill(-1);
      inQueue.fill(false);
      dist[source] = 0;
      const queue: number[] = [source];
      inQueue[source] = true;
      let head = 0;
      while (head < queue.length) {
        const v = queue[head++];
        inQueue[v] = false;
        const edges = this.graph[v];
        for (let i = 0; i < edges.length; i++) {
          const e = edges[i];
          if (e.cap <= 0) continue;
          const nd = dist[v] + e.cost;
          if (nd < dist[e.to]) {
            dist[e.to] = nd;
            prevNode[e.to] = v;
            prevEdge[e.to] = i;
            if (!inQueue[e.to]) {
              queue.push(e.to);
              inQueue[e.to] = true;
            }
          }
        }
      }
      if (prevNode[sink] < 0) break;

      let d = flowLimit - flow;
      for (let v = sink; v !== source; v = prevNode[v]) {
        d = Math.min(d, this.graph[prevNode[v]][prevEdge[v]].cap);
      }
      for (let v = sink; v !== source; v = prevNode[v]) {
        const e = this.graph[prevNode[v]][prevEdge[v]];
        e.cap -= d;
        this.graph[v][e.rev].cap += d;
      }
      flow += d;
      cost += d * dist[sink];
    }
    return { flow, cost };
  }
}
