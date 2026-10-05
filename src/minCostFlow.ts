/** 残量网络边。 */
export interface FlowEdge {
  to: number;
  /** 反向边在邻接表中的下标。 */
  rev: number;
  cap: number;
  cost: number;
}

/**
 * 整数最小费用最大流：连续最短路（SSP）+ Johnson 势函数。
 * 要求所有原始边费用非负（电价 ≥ 0），因此初始势全为 0 即可使用 Dijkstra。
 * 所有容量与费用均为整数，计算过程保持整数精确。
 */
export class MinCostFlow {
  private readonly graph: FlowEdge[][];

  constructor(nodeCount: number) {
    this.graph = Array.from({ length: nodeCount }, () => []);
  }

  /** 添加一条边，返回正向边引用（求解后可通过 原容量 - edge.cap 读取流量）。 */
  addEdge(from: number, to: number, cap: number, cost: number): FlowEdge {
    const forward: FlowEdge = { to, rev: this.graph[to].length, cap, cost };
    const backward: FlowEdge = { to: from, rev: this.graph[from].length, cap: 0, cost: -cost };
    this.graph[from].push(forward);
    this.graph[to].push(backward);
    return forward;
  }

  /** 在流量不超过 maxFlow 的前提下求最小费用流，返回实际流量与费用。 */
  minCostMaxFlow(source: number, sink: number, maxFlow: number): { flow: number; cost: number } {
    const nodeCount = this.graph.length;
    const potential = new Array<number>(nodeCount).fill(0);
    const dist = new Array<number>(nodeCount);
    const prevNode = new Array<number>(nodeCount);
    const prevEdge = new Array<number>(nodeCount);
    let flow = 0;
    let cost = 0;

    while (flow < maxFlow) {
      dist.fill(Infinity);
      dist[source] = 0;
      // 二叉堆实现的 Dijkstra，堆元素为 [距离, 节点]
      const heap: [number, number][] = [[0, source]];
      while (heap.length > 0) {
        const [d, v] = heapPop(heap);
        if (d !== dist[v]) continue;
        const edges = this.graph[v];
        for (let i = 0; i < edges.length; i++) {
          const e = edges[i];
          if (e.cap <= 0) continue;
          const nd = d + e.cost + potential[v] - potential[e.to];
          if (nd < dist[e.to]) {
            dist[e.to] = nd;
            prevNode[e.to] = v;
            prevEdge[e.to] = i;
            heapPush(heap, [nd, e.to]);
          }
        }
      }
      if (dist[sink] === Infinity) break;
      for (let v = 0; v < nodeCount; v++) {
        if (dist[v] < Infinity) potential[v] += dist[v];
      }
      let add = maxFlow - flow;
      for (let v = sink; v !== source; v = prevNode[v]) {
        add = Math.min(add, this.graph[prevNode[v]][prevEdge[v]].cap);
      }
      for (let v = sink; v !== source; v = prevNode[v]) {
        const e = this.graph[prevNode[v]][prevEdge[v]];
        e.cap -= add;
        this.graph[v][e.rev].cap += add;
      }
      cost += add * (potential[sink] - potential[source]);
      flow += add;
    }
    return { flow, cost };
  }
}

function heapPush(heap: [number, number][], item: [number, number]): void {
  heap.push(item);
  let i = heap.length - 1;
  while (i > 0) {
    const parent = (i - 1) >> 1;
    if (heap[parent][0] <= heap[i][0]) break;
    [heap[parent], heap[i]] = [heap[i], heap[parent]];
    i = parent;
  }
}

function heapPop(heap: [number, number][]): [number, number] {
  const top = heap[0];
  const last = heap.pop() as [number, number];
  if (heap.length > 0) {
    heap[0] = last;
    let i = 0;
    for (;;) {
      const left = i * 2 + 1;
      const right = left + 1;
      let smallest = i;
      if (left < heap.length && heap[left][0] < heap[smallest][0]) smallest = left;
      if (right < heap.length && heap[right][0] < heap[smallest][0]) smallest = right;
      if (smallest === i) break;
      [heap[smallest], heap[i]] = [heap[i], heap[smallest]];
      i = smallest;
    }
  }
  return top;
}
