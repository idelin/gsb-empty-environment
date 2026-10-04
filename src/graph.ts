import type { AgentId, ChainStep } from './types.ts';

export interface AttributionEdge {
  from: AgentId;
  to: AgentId;
  rateBps: number;
}

export interface RawChain {
  recipient: AgentId;
  steps: ChainStep[];
  /** 沿途 rateBps 的整数连乘（精确值，避免浮点误差） */
  rateProduct: bigint;
  depth: number;
}

const BPS_BASE = 10000n;

/**
 * 有向无环归属图。边 from -> to 表示 "to 是 from 的直接上级"，
 * 佣金沿边向上游传导，有效费率为沿途费率连乘。
 */
export class AttributionGraph {
  private readonly adjacency = new Map<AgentId, AttributionEdge[]>();

  addEdge(from: AgentId, to: AgentId, rateBps: number): void {
    if (from === to) {
      throw new Error(`self edge not allowed: ${from}`);
    }
    if (!Number.isInteger(rateBps) || rateBps < 0) {
      throw new Error(`rateBps must be a non-negative integer, got ${rateBps}`);
    }
    const edges = this.adjacency.get(from) ?? [];
    if (edges.some((e) => e.to === to)) {
      throw new Error(`duplicate edge ${from} -> ${to}`);
    }
    // 加边 from->to 会成环，当且仅当图中已存在 to 到 from 的通路
    if (this.reachable(to, from)) {
      throw new Error(`edge ${from} -> ${to} would create a cycle`);
    }
    edges.push({ from, to, rateBps });
    this.adjacency.set(from, edges);
  }

  edgesFrom(node: AgentId): readonly AttributionEdge[] {
    return this.adjacency.get(node) ?? [];
  }

  private reachable(start: AgentId, target: AgentId): boolean {
    const stack = [start];
    const seen = new Set<AgentId>();
    while (stack.length > 0) {
      const node = stack.pop()!;
      if (node === target) return true;
      if (seen.has(node)) continue;
      seen.add(node);
      for (const e of this.edgesFrom(node)) stack.push(e.to);
    }
    return false;
  }

  /** 枚举从 start 出发的全部来源链（每个上级一条或多条候选链） */
  enumerateRawChains(start: AgentId): RawChain[] {
    const out: RawChain[] = [];
    const walk = (node: AgentId, steps: ChainStep[], product: bigint): void => {
      for (const e of this.edgesFrom(node)) {
        const nextSteps = [...steps, { from: e.from, to: e.to, rateBps: e.rateBps }];
        const nextProduct = product * BigInt(e.rateBps);
        out.push({
          recipient: e.to,
          steps: nextSteps,
          rateProduct: nextProduct,
          depth: nextSteps.length,
        });
        walk(e.to, nextSteps, nextProduct);
      }
    };
    walk(start, [], 1n);
    return out;
  }

  /** 精确比较两条链的有效费率：a 与 b 谁大 */
  static compareRates(a: RawChain, b: RawChain): number {
    const lhs = a.rateProduct * BPS_BASE ** BigInt(b.depth);
    const rhs = b.rateProduct * BPS_BASE ** BigInt(a.depth);
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  /** 由费率连乘折合的有效费率（万分比，展示用） */
  static effectiveRateBps(chain: RawChain): number {
    return Number(chain.rateProduct) / Number(BPS_BASE ** BigInt(chain.depth - 1));
  }

  /** 按链计算佣金金额：floor(amount * 有效费率)，全程整数运算 */
  static amountFor(amount: number, chain: RawChain): number {
    const value =
      (BigInt(amount) * chain.rateProduct) / BPS_BASE ** BigInt(chain.depth);
    return Number(value);
  }
}
