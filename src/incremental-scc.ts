/**
 * 增量强连通分量(SCC)维护器。
 *
 * 设计要点:
 * - 增量缩点: 始终维护当前图的缩点(condensation), 每个分量以其成员中的最小节点 ID 作为稳定标识。
 * - 边插入: 在缩点上做局部前向/后向遍历, 仅当 v 可达 u 时, 将"位于 v~>u 路径上"的分量合并。
 * - 边删除: 仅在受影响的原分量内部做局部 Tarjan 遍历, 按需分裂, 不触碰无关分量。
 * - 每次变更返回 SCCDiff, 描述合并/分裂/移除的分量差量。
 */

export interface ComponentInfo {
  /** 分量标识 = 成员中的最小节点 ID(稳定) */
  id: string;
  /** 分量成员(按字典序排序) */
  members: string[];
}

export interface MergeRecord {
  /** 合并后的新分量 */
  component: ComponentInfo;
  /** 被合并吸收的原分量 */
  absorbed: ComponentInfo[];
}

export interface SplitRecord {
  /** 分裂前的原分量 */
  from: ComponentInfo;
  /** 分裂产生的新分量 */
  components: ComponentInfo[];
}

export interface SCCDiff {
  merged: MergeRecord[];
  split: SplitRecord[];
  /** 因顶点删除而消失的分量 */
  removed: ComponentInfo[];
}

function emptyDiff(): SCCDiff {
  return { merged: [], split: [], removed: [] };
}

function isEmptyDiff(diff: SCCDiff): boolean {
  return diff.merged.length === 0 && diff.split.length === 0 && diff.removed.length === 0;
}

function minId(members: Iterable<string>): string {
  let min: string | undefined;
  for (const m of members) {
    if (min === undefined || m < min) min = m;
  }
  return min as string;
}

export class DynamicSCC {
  private out = new Map<string, Set<string>>();
  private inn = new Map<string, Set<string>>();
  private compOf = new Map<string, string>();
  private components = new Map<string, Set<string>>();
  private lastDiff: SCCDiff = emptyDiff();

  /** 新增顶点(已存在则忽略), 初始为单节点分量 */
  addVertex(v: string): SCCDiff {
    if (this.out.has(v)) {
      this.lastDiff = emptyDiff();
      return this.lastDiff;
    }
    this.out.set(v, new Set());
    this.inn.set(v, new Set());
    this.compOf.set(v, v);
    this.components.set(v, new Set([v]));
    this.lastDiff = emptyDiff();
    return this.lastDiff;
  }

  /** 删除顶点及其所有关联边, 返回累积差量 */
  removeVertex(v: string): SCCDiff {
    const combined = emptyDiff();
    if (!this.out.has(v)) {
      this.lastDiff = combined;
      return combined;
    }
    for (const w of [...this.out.get(v)!]) {
      this.accumulate(combined, this.removeEdge(v, w));
    }
    for (const u of [...this.inn.get(v)!]) {
      this.accumulate(combined, this.removeEdge(u, v));
    }
    // 所有关联边删除后, v 必为孤立单节点分量
    const id = this.compOf.get(v)!;
    combined.removed.push({ id, members: [v] });
    this.components.delete(id);
    this.compOf.delete(v);
    this.out.delete(v);
    this.inn.delete(v);
    this.lastDiff = combined;
    return combined;
  }

  /** 插入有向边 u->v(端点不存在时自动补建), 返回合并差量 */
  addEdge(u: string, v: string): SCCDiff {
    this.addVertexIfAbsent(u);
    this.addVertexIfAbsent(v);
    const diff = emptyDiff();
    if (u === v || this.out.get(u)!.has(v)) {
      this.lastDiff = diff;
      return diff;
    }
    this.out.get(u)!.add(v);
    this.inn.get(v)!.add(u);

    const cu = this.compOf.get(u)!;
    const cv = this.compOf.get(v)!;
    if (cu === cv) {
      this.lastDiff = diff;
      return diff;
    }

    // 局部前向遍历: 从 cv 出发在缩点上可达的分量集合
    const forward = this.reachableComponents(cv, this.out);
    if (!forward.has(cu)) {
      // v 不可达 u, 未形成环, 无合并
      this.lastDiff = diff;
      return diff;
    }
    // 局部后向遍历: 可到达 cu 的分量集合
    const backward = this.reachableComponents(cu, this.inn);

    // 位于 v~>u 路径上的分量 = forward ∩ backward, 合并为一个新分量
    const absorbed: ComponentInfo[] = [];
    const members = new Set<string>();
    for (const id of forward) {
      if (!backward.has(id)) continue;
      const ms = this.components.get(id)!;
      absorbed.push({ id, members: [...ms].sort() });
      for (const m of ms) members.add(m);
    }
    absorbed.sort((a, b) => (a.id < b.id ? -1 : 1));

    const newId = minId(members);
    for (const a of absorbed) this.components.delete(a.id);
    this.components.set(newId, members);
    for (const m of members) this.compOf.set(m, newId);

    diff.merged.push({
      component: { id: newId, members: [...members].sort() },
      absorbed,
    });
    this.lastDiff = diff;
    return diff;
  }

  /** 删除有向边 u->v, 返回分裂差量 */
  removeEdge(u: string, v: string): SCCDiff {
    const diff = emptyDiff();
    if (!this.out.get(u)?.has(v)) {
      this.lastDiff = diff;
      return diff;
    }
    this.out.get(u)!.delete(v);
    this.inn.get(v)!.delete(u);

    const cu = this.compOf.get(u)!;
    const cv = this.compOf.get(v)!;
    if (cu !== cv) {
      this.lastDiff = diff;
      return diff;
    }

    // 仅在受影响的原分量内部做局部 Tarjan, 判断是否分裂
    const members = this.components.get(cu)!;
    const sccs = this.localTarjan(members);
    if (sccs.length === 1) {
      this.lastDiff = diff;
      return diff;
    }

    const from: ComponentInfo = { id: cu, members: [...members].sort() };
    this.components.delete(cu);
    const produced: ComponentInfo[] = [];
    for (const scc of sccs) {
      const id = minId(scc);
      const set = new Set(scc);
      this.components.set(id, set);
      for (const m of set) this.compOf.set(m, id);
      produced.push({ id, members: [...set].sort() });
    }
    produced.sort((a, b) => (a.id < b.id ? -1 : 1));
    diff.split.push({ from, components: produced });
    this.lastDiff = diff;
    return diff;
  }

  /** 查询顶点所属分量 ID(最小节点 ID), 顶点不存在返回 undefined */
  componentOf(v: string): string | undefined {
    return this.compOf.get(v);
  }

  /** 当前全部分量快照(按 ID 排序) */
  getComponents(): ComponentInfo[] {
    return [...this.components.entries()]
      .map(([id, members]) => ({ id, members: [...members].sort() }))
      .sort((a, b) => (a.id < b.id ? -1 : 1));
  }

  /** 最近一次变更操作产生的差量 */
  getLastDiff(): SCCDiff {
    return this.lastDiff;
  }

  hasVertex(v: string): boolean {
    return this.out.has(v);
  }

  hasEdge(u: string, v: string): boolean {
    return this.out.get(u)?.has(v) ?? false;
  }

  private addVertexIfAbsent(v: string): void {
    if (!this.out.has(v)) this.addVertex(v);
  }

  private accumulate(target: SCCDiff, part: SCCDiff): void {
    target.merged.push(...part.merged);
    target.split.push(...part.split);
    target.removed.push(...part.removed);
  }

  /** 在缩点上从 start 分量出发做局部 BFS, 返回可达分量 ID 集合(含自身) */
  private reachableComponents(
    start: string,
    adj: Map<string, Set<string>>,
  ): Set<string> {
    const visited = new Set<string>([start]);
    const queue: string[] = [];
    for (const m of this.components.get(start)!) queue.push(m);
    while (queue.length > 0) {
      const x = queue.pop()!;
      for (const y of adj.get(x)!) {
        const cy = this.compOf.get(y)!;
        if (!visited.has(cy)) {
          visited.add(cy);
          for (const m of this.components.get(cy)!) queue.push(m);
        }
      }
    }
    return visited;
  }

  /** 在 members 诱导子图上做迭代式 Tarjan, 返回 SCC 列表 */
  private localTarjan(members: Set<string>): string[][] {
    const index = new Map<string, number>();
    const low = new Map<string, number>();
    const onStack = new Set<string>();
    const stack: string[] = [];
    const result: string[][] = [];
    let counter = 0;

    const sorted = [...members].sort();
    for (const start of sorted) {
      if (index.has(start)) continue;
      index.set(start, counter);
      low.set(start, counter);
      counter++;
      stack.push(start);
      onStack.add(start);
      const work: Array<[string, string[], number]> = [
        [start, this.neighborsWithin(start, members), 0],
      ];
      while (work.length > 0) {
        const frame = work[work.length - 1];
        const [node, nbrs, pos] = frame;
        if (pos < nbrs.length) {
          frame[2] = pos + 1;
          const w = nbrs[pos];
          if (!index.has(w)) {
            index.set(w, counter);
            low.set(w, counter);
            counter++;
            stack.push(w);
            onStack.add(w);
            work.push([w, this.neighborsWithin(w, members), 0]);
          } else if (onStack.has(w)) {
            low.set(node, Math.min(low.get(node)!, index.get(w)!));
          }
        } else {
          work.pop();
          if (work.length > 0) {
            const parent = work[work.length - 1][0];
            low.set(parent, Math.min(low.get(parent)!, low.get(node)!));
          }
          if (low.get(node) === index.get(node)) {
            const scc: string[] = [];
            let w: string;
            do {
              w = stack.pop()!;
              onStack.delete(w);
              scc.push(w);
            } while (w !== node);
            result.push(scc);
          }
        }
      }
    }
    return result;
  }

  private neighborsWithin(v: string, members: Set<string>): string[] {
    const out = this.out.get(v)!;
    const result: string[] = [];
    for (const w of out) {
      if (members.has(w)) result.push(w);
    }
    result.sort();
    return result;
  }
}

export { isEmptyDiff };
