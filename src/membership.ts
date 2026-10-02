/**
 * 动态分片：用 Rendezvous（HRW）哈希把全网节点划分为虚拟局部集群（Cluster Cell）。
 *
 * - 每个节点对 (nodeId, cellSlot) 打分，加入得分最高的 Cell，无需中心协调。
 * - Cell 数量变化（节点规模伸缩 → ceil(sqrt(N)) 调整）时，理论上只有
 *   ~1/(k+1) 的节点迁移，分片是"动态但低扰动"的。
 */

import { hash32 } from './rng.ts';

export class CellAssignment {
  readonly numCells: number;
  private cache = new Map<number, number>();

  constructor(numCells: number) {
    this.numCells = Math.max(1, Math.floor(numCells));
  }

  /** 推荐的 Cell 数量：~sqrt(N)，使 Cell 内与跨 Cell 流量都保持在 O(sqrt(N))。 */
  static recommendedCells(nodeCount: number): number {
    return Math.max(1, Math.round(Math.sqrt(nodeCount)));
  }

  cellOf(nodeId: number): number {
    const cached = this.cache.get(nodeId);
    if (cached !== undefined) return cached;
    let best = 0;
    let bestScore = -1;
    for (let c = 0; c < this.numCells; c++) {
      const score = hash32(`${nodeId}:${c}`);
      if (score > bestScore) {
        bestScore = score;
        best = c;
      }
    }
    this.cache.set(nodeId, best);
    return best;
  }

  /** 把节点划分为 numCells 个 Cell，返回按 id 升序的分组。 */
  partition(nodeIds: number[]): number[][] {
    const cells: number[][] = Array.from({ length: this.numCells }, () => []);
    for (const id of nodeIds) cells[this.cellOf(id)].push(id);
    for (const cell of cells) cell.sort((a, b) => a - b);
    return cells;
  }
}
