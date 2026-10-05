/**
 * 带边流量下界的最小费用流（SSP + Johnson 势能，费用非负）。
 *
 * 用法：addEdge(from, to, lower, upper, cost) 添加带下界的边，
 * solve() 自动追加超级源/汇把下界转化为节点供需，求满足所有下界且
 * 总费用最小的可行循环流；不可行时返回 null。
 */

interface FlowEdge {
  to: number;
  rev: number;
  cap: number;
  cost: number;
}

export interface EdgeHandle {
  from: number;
  index: number;
  lower: number;
  initialCap: number;
}

export class MinCostFlow {
  private graph: FlowEdge[][];
  private balances: number[];
  private readonly nodeCount: number;
  private readonly superSource: number;
  private readonly superSink: number;

  constructor(numNodes: number) {
    this.nodeCount = numNodes;
    this.superSource = numNodes;
    this.superSink = numNodes + 1;
    this.graph = [];
    for (let i = 0; i < numNodes + 2; i++) this.graph.push([]);
    this.balances = new Array(numNodes).fill(0);
  }

  addEdge(from: number, to: number, lower: number, upper: number, cost: number): EdgeHandle {
    if (!Number.isFinite(lower) || lower < 0 || upper < lower) {
      throw new Error(`invalid bounds [${lower}, ${upper}]`);
    }
    if (cost < 0) throw new Error('negative edge costs are not supported');
    const fwd: FlowEdge = { to, rev: this.graph[to].length, cap: upper - lower, cost };
    const rev: FlowEdge = { to: from, rev: this.graph[from].length, cap: 0, cost: -cost };
    this.graph[from].push(fwd);
    this.graph[to].push(rev);
    this.balances[from] -= lower;
    this.balances[to] += lower;
    return { from, index: this.graph[from].length - 1, lower, initialCap: upper - lower };
  }

  private addRawEdge(from: number, to: number, cap: number, cost: number): void {
    const fwd: FlowEdge = { to, rev: this.graph[to].length, cap, cost };
    const rev: FlowEdge = { to: from, rev: this.graph[from].length, cap: 0, cost: -cost };
    this.graph[from].push(fwd);
    this.graph[to].push(rev);
  }

  /** 边上的实际流量（含下界）。 */
  edgeFlow(handle: EdgeHandle): number {
    return handle.lower + (handle.initialCap - this.graph[handle.from][handle.index].cap);
  }

  /**
   * 求满足所有边下界的最小费用可行流。
   * 返回最小总费用；无可行流时返回 null。
   */
  solve(): number | null {
    let required = 0;
    for (let v = 0; v < this.nodeCount; v++) {
      const balance = this.balances[v];
      if (balance > 0) {
        this.addRawEdge(this.superSource, v, balance, 0);
        required += balance;
      } else if (balance < 0) {
        this.addRawEdge(v, this.superSink, -balance, 0);
      }
    }
    const { flow, cost } = this.minCostMaxFlow(this.superSource, this.superSink);
    if (flow < required) return null;
    return cost;
  }

  private minCostMaxFlow(source: number, sink: number): { flow: number; cost: number } {
    const n = this.graph.length;
    const potential = new Array<number>(n).fill(0);
    let flow = 0;
    let cost = 0;
    for (;;) {
      const dist = new Array<number>(n).fill(Infinity);
      const prevNode = new Array<number>(n).fill(-1);
      const prevEdge = new Array<number>(n).fill(-1);
      const done = new Array<boolean>(n).fill(false);
      dist[source] = 0;
      for (;;) {
        let u = -1;
        let best = Infinity;
        for (let i = 0; i < n; i++) {
          if (!done[i] && dist[i] < best) {
            best = dist[i];
            u = i;
          }
        }
        if (u === -1) break;
        done[u] = true;
        const edges = this.graph[u];
        for (let i = 0; i < edges.length; i++) {
          const e = edges[i];
          if (e.cap <= 0) continue;
          const nd = dist[u] + e.cost + potential[u] - potential[e.to];
          if (nd < dist[e.to]) {
            dist[e.to] = nd;
            prevNode[e.to] = u;
            prevEdge[e.to] = i;
          }
        }
      }
      if (dist[sink] === Infinity) break;
      for (let v = 0; v < n; v++) {
        if (dist[v] < Infinity) potential[v] += dist[v];
      }
      let add = Infinity;
      for (let v = sink; v !== source; v = prevNode[v]) {
        add = Math.min(add, this.graph[prevNode[v]][prevEdge[v]].cap);
      }
      for (let v = sink; v !== source; v = prevNode[v]) {
        const e = this.graph[prevNode[v]][prevEdge[v]];
        e.cap -= add;
        this.graph[v][e.rev].cap += add;
        cost += add * e.cost;
      }
      flow += add;
    }
    return { flow, cost };
  }
}
