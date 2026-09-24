/**
 * 固定驱逐策略（唯一、确定、可陈述）：
 *   候选集 = 已缓存的派生值中“未被订阅”的项（源状态与订阅保留值永远不是候选）；
 *   权重   = 命中次数 hits，权重最低者先被驱逐；
 *   并列   = 最久未使用（lastUsed 小）者优先，再按节点 id 字典序。
 * 被驱逐的只是可重算的缓存值，源状态与依赖版本账本不受影响。
 */

export interface EvictableEntry {
  nodeId: string;
  hits: number;
  lastUsed: number;
  pinned: boolean;
}

export function chooseVictim(entries: readonly EvictableEntry[]): string | null {
  let victim: EvictableEntry | null = null;
  for (const e of entries) {
    if (e.pinned) continue;
    if (
      victim === null ||
      e.hits < victim.hits ||
      (e.hits === victim.hits &&
        (e.lastUsed < victim.lastUsed ||
          (e.lastUsed === victim.lastUsed && e.nodeId < victim.nodeId)))
    ) {
      victim = e;
    }
  }
  return victim === null ? null : victim.nodeId;
}
