import type { CfgEdge, CfgNode, EdgeKind, Operation, ProcedureGraph } from './types.ts';

/**
 * 过程图构建器：维护 region 嵌套，自动为异常边计算 LIFO 延迟清理序列。
 * 前置条件（结构化控制流）：deferClose 位于可能抛出的操作之前并支配它，
 * 因此异常展开时该注册必然已生效。
 */
export class GraphBuilder {
  private nodes: CfgNode[] = [];
  private edges: CfgEdge[] = [];
  private entry: string | null = null;
  /** 每个 region 内按声明顺序注册的延迟关闭变量。 */
  private regionDefs = new Map<string, string[]>();

  addNode(id: string, operation: Operation): this {
    if (this.entry === null) this.entry = id;
    this.nodes.push({ id, operation });
    if (operation.op === 'deferClose') {
      const list = this.regionDefs.get(operation.region);
      if (list) list.push(operation.variable);
      else this.regionDefs.set(operation.region, [operation.variable]);
    }
    return this;
  }

  addEdge(from: string, to: string, kind: EdgeKind = 'normal', unwindThroughRegion?: string): this {
    const edge: CfgEdge = { from, to, kind, unwindThroughRegion };
    this.edges.push(edge);
    return this;
  }

  build(): ProcedureGraph {
    if (this.entry === null) throw new Error('GraphBuilder: 空图');
    const ids = new Set(this.nodes.map((n) => n.id));
    for (const edge of this.edges) {
      if (!ids.has(edge.from) || !ids.has(edge.to)) throw new Error(`GraphBuilder: 悬空边 ${edge.from}->${edge.to}`);
    }
    const graph: ProcedureGraph = { nodes: this.nodes, edges: this.edges, entry: this.entry };
    const snapshots = this.collectSnapshots(graph);
    for (const edge of this.edges) {
      if (edge.kind !== 'exception') continue;
      const snapshot = snapshots.get(edge.from);
      if (!snapshot) {
        edge.cleanup = [];
        continue;
      }
      const limit = edge.unwindThroughRegion
        ? snapshot.stack.lastIndexOf(edge.unwindThroughRegion)
        : snapshot.stack.length - 1;
      edge.cleanedRegions =
        limit >= 0
          ? edge.unwindThroughRegion
            ? [...snapshot.stack.slice(limit)]
            : [...snapshot.stack]
          : [];
      edge.cleanup = this.unwind(snapshot.stack, snapshot.pending, edge.unwindThroughRegion);
    }
    return graph;
  }

  /**
   * 沿正常边 DFS，记录每个（正常可达）节点处的 region 栈与已注册延迟关闭快照。
   * 异常边不参与 DFS（它们是展开边）。结构化控制流下各路径快照一致；
   * 循环回边只重复进入同一组 region，首次访问快照即可代表。
   */
  private collectSnapshots(graph: ProcedureGraph): Map<string, { stack: string[]; pending: Map<string, string[]> }> {
    const nodeById = new Map(graph.nodes.map((n) => [n.id, n]));
    const allOut = new Map<string, CfgEdge[]>();
    for (const node of graph.nodes) allOut.set(node.id, []);
    for (const edge of graph.edges) {
      allOut.get(edge.from)!.push(edge);
    }
    const snapshots = new Map<string, { stack: string[]; pending: Map<string, string[]> }>();
    const dfs = (id: string, stack: string[], pending: Map<string, string[]>): void => {
      if (snapshots.has(id)) return;
      snapshots.set(id, { stack: [...stack], pending: new Map([...pending].map(([k, v]) => [k, [...v]])) });
      const node = nodeById.get(id)!;
      const nextStack = [...stack];
      const nextPending = new Map([...pending].map(([k, v]) => [k, [...v]]));
      if (node.operation.op === 'enterRegion') nextStack.push(node.operation.region);
      if (node.operation.op === 'deferClose') {
        const list = nextPending.get(node.operation.region);
        if (list) list.push(node.operation.variable);
        else nextPending.set(node.operation.region, [node.operation.variable]);
      }
      if (node.operation.op === 'exitRegion') {
        const idx = nextStack.lastIndexOf(node.operation.region);
        if (idx >= 0) {
          for (const region of nextStack.splice(idx)) nextPending.delete(region);
        }
      }
      // 异常边同样要遍历以覆盖处理器内节点，但不沿它传播 region 快照
      // （异常边的快照取自源点；进入处理器视为在新帧继续）。
      for (const edge of allOut.get(id) ?? []) {
        if (edge.kind === 'exception') {
          if (!snapshots.has(edge.to)) {
            // 处理器帧保留展开边界之外的外层 region（重抛时还要展开它们），
            // 已展开 region 的 pending 已随异常边注销，因此处理器 pending 从空开始。
            const limit = edge.unwindThroughRegion
              ? nextStack.lastIndexOf(edge.unwindThroughRegion)
              : nextStack.length - 1;
            const residualStack =
              limit >= 0
                ? edge.unwindThroughRegion
                  ? [...nextStack.slice(0, limit)]
                  : []
                : [...nextStack];
            const residualPending = new Map<string, string[]>();
            for (const region of residualStack) {
              const list = nextPending.get(region);
              if (list) residualPending.set(region, [...list]);
            }
            dfs(edge.to, residualStack, residualPending);
          }
        } else {
          dfs(edge.to, nextStack, nextPending);
        }
      }
    };
    dfs(graph.entry, [], new Map());
    return snapshots;
  }

  /**
   * region 内按 LIFO、region 间按内层到外层展开。
   * throughRegion 给出展开到哪个 region（含）为止；缺省展开整个活动栈。
   * 处理器内重抛时内层 region 已在进入处理器前展开，因此只能展开剩余外层。
   */
  private unwind(stack: string[], pending: Map<string, string[]>, throughRegion?: string): string[] {
    const out: string[] = [];
    const limit = throughRegion ? stack.lastIndexOf(throughRegion) : stack.length - 1;
    if (limit < 0) return out;
    // 无 through 时展开整个活动栈；否则展开栈顶到 through（含），外层留给后续重抛。
    const unwindRegions = throughRegion ? stack.slice(limit) : [...stack];
    for (const region of [...unwindRegions].reverse()) {
      const list = pending.get(region) ?? this.regionDefs.get(region);
      if (list) out.push(...[...list].reverse());
    }
    return out;
  }
}
