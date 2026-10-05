// 变更流的物化视图：以稳定告警 ID 为键应用 upsert/retract。
// 重复消费同一变更流是幂等的（upsert 覆盖、retract 删除），不会重复计数。

import type { Change } from './types.ts';

export class AlertView {
  private map = new Map<string, { aId: string; bId: string }>();

  apply(changes: Iterable<Change>): void {
    for (const c of changes) {
      if (c.type === 'upsert') this.map.set(c.alertId, { aId: c.aId, bId: c.bId });
      else this.map.delete(c.alertId);
    }
  }

  get size(): number {
    return this.map.size;
  }

  entries(): Map<string, { aId: string; bId: string }> {
    return new Map(this.map);
  }

  pairSet(): Set<string> {
    return new Set([...this.map.values()].map((v) => `${v.aId}|${v.bId}`));
  }
}
