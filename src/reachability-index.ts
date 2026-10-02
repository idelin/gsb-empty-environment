import { CompressedBitmap } from "./compressed-bitmap.ts";

/**
 * 有向无环图可达性位图索引器。
 *
 * - 每个节点维护一个压缩位图 reach[u]，记录 u 的全部下游可达节点（含自身）。
 * - 查询 reachable(u, v) 即一次位测试，O(1)。
 * - 插入边 (u, v) 时，沿父链收集 u 的全部祖先 a，执行 reach[a] |= reach[v]，
 *   合并以 256 bit 块为单位做字级并行 OR，增量维护传递闭包。
 * - 拓扑层级 level 随边插入增量提升：level[v] = max(level[v], level[u] + 1) 并向下游传播。
 */
export class DagReachabilityIndex {
  private ids = new Map<string, number>();
  private labels: string[] = [];
  private children: number[][] = [];
  private parents: number[][] = [];
  private levels: number[] = [];
  private reach: CompressedBitmap[] = [];

  addNode(label: string): number {
    const existing = this.ids.get(label);
    if (existing !== undefined) return existing;
    const id = this.labels.length;
    this.ids.set(label, id);
    this.labels.push(label);
    this.children.push([]);
    this.parents.push([]);
    this.levels.push(0);
    const bitmap = new CompressedBitmap();
    bitmap.set(id);
    this.reach.push(bitmap);
    return id;
  }

  addEdge(fromLabel: string, toLabel: string): void {
    const u = this.addNode(fromLabel);
    const v = this.addNode(toLabel);
    if (u === v) throw new Error(`self-loop on ${fromLabel} would create a cycle`);
    if (this.reach[v].has(u)) {
      throw new Error(`edge ${fromLabel} -> ${toLabel} would create a cycle`);
    }
    if (this.reach[u].has(v)) return;

    this.children[u].push(v);
    this.parents[v].push(u);

    const downstream = this.reach[v];
    const visited = new Set<number>();
    const stack = [u];
    while (stack.length > 0) {
      const a = stack.pop()!;
      if (visited.has(a)) continue;
      visited.add(a);
      this.reach[a].unionFrom(downstream);
      for (const p of this.parents[a]) stack.push(p);
    }

    if (this.levels[v] < this.levels[u] + 1) {
      const queue: number[] = [v];
      this.levels[v] = this.levels[u] + 1;
      while (queue.length > 0) {
        const cur = queue.shift()!;
        for (const next of this.children[cur]) {
          if (this.levels[next] < this.levels[cur] + 1) {
            this.levels[next] = this.levels[cur] + 1;
            queue.push(next);
          }
        }
      }
    }
  }

  /** O(1) 可达性查询：单次位测试。 */
  reachable(fromLabel: string, toLabel: string): boolean {
    const u = this.ids.get(fromLabel);
    const v = this.ids.get(toLabel);
    if (u === undefined || v === undefined) return false;
    return this.reach[u].has(v);
  }

  levelOf(label: string): number {
    const id = this.ids.get(label);
    if (id === undefined) throw new Error(`unknown node ${label}`);
    return this.levels[id];
  }

  /** 按拓扑层级分组的节点标签，层级即最长路径分层。 */
  layers(): string[][] {
    const result: string[][] = [];
    for (let id = 0; id < this.labels.length; id++) {
      const level = this.levels[id];
      while (result.length <= level) result.push([]);
      result[level].push(this.labels[id]);
    }
    return result;
  }

  reachableSet(label: string): string[] {
    const id = this.ids.get(label);
    if (id === undefined) throw new Error(`unknown node ${label}`);
    return [...this.reach[id].bits()].map((bit) => this.labels[bit]);
  }

  nodeCount(): number {
    return this.labels.length;
  }

  /** 索引实际内存占用（全部压缩位图字节数之和）。 */
  memoryBytes(): number {
    let total = 0;
    for (const bitmap of this.reach) total += bitmap.byteSize();
    return total;
  }

  /** 未压缩稠密位矩阵的字节数上界，用于对比压缩率。 */
  denseMatrixBytes(): number {
    const n = this.labels.length;
    return n * Math.ceil(n / 32) * 4;
  }
}
