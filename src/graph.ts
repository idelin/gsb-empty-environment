import type { AgentId, SourceChain } from './types.ts';

/**
 * 有向无环归属图。
 * 边 from -> to 表示「to 是 from 的直接上级」，交易佣金沿边向上流动，
 * 边上费率 rate ∈ (0, 1]，路径费率为各边费率乘积。
 */
export class AttributionGraph {
  private readonly uplines = new Map<AgentId, Map<AgentId, number>>();

  addNode(id: AgentId): void {
    if (!this.uplines.has(id)) this.uplines.set(id, new Map());
  }

  hasNode(id: AgentId): boolean {
    return this.uplines.has(id);
  }

  /** 登记 from 的直接上级 to 及该边费率；会破坏 DAG（成环/自环）时抛错 */
  addEdge(from: AgentId, to: AgentId, rate: number): void {
    if (from === to) throw new Error(`self-loop edge: ${from}`);
    if (!Number.isFinite(rate) || rate <= 0 || rate > 1) {
      throw new Error(`edge rate must be in (0, 1], got ${rate}`);
    }
    this.addNode(from);
    this.addNode(to);
    if (this.reachable(to, from)) {
      throw new Error(`edge ${from} -> ${to} would create a cycle`);
    }
    this.uplines.get(from)!.set(to, rate);
  }

  directUplines(id: AgentId): ReadonlyMap<AgentId, number> {
    return this.uplines.get(id) ?? new Map();
  }

  /** 沿上级边从 from 出发能否到达 target */
  reachable(from: AgentId, target: AgentId): boolean {
    const seen = new Set<AgentId>();
    const stack = [from];
    while (stack.length > 0) {
      const cur = stack.pop()!;
      if (cur === target) return true;
      if (seen.has(cur)) continue;
      seen.add(cur);
      for (const up of this.directUplines(cur).keys()) stack.push(up);
    }
    return false;
  }

  /**
   * 枚举从 source 出发沿上级边到达每个上级的全部简单路径。
   * 返回 upline -> 该上级的所有来源链（同一上级可经多条路径到达）。
   */
  enumerateChains(source: AgentId): Map<AgentId, SourceChain[]> {
    const result = new Map<AgentId, SourceChain[]>();
    const visit = (node: AgentId, path: AgentId[], rate: number): void => {
      for (const [up, edgeRate] of this.directUplines(node)) {
        const chain: SourceChain = { path: [...path, up], rate: rate * edgeRate };
        const list = result.get(up);
        if (list) list.push(chain);
        else result.set(up, [chain]);
        visit(up, chain.path, chain.rate);
      }
    };
    visit(source, [source], 1);
    return result;
  }
}
