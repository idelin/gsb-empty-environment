import type { FrameId, GrantKey, Site } from "./types.ts";

/**
 * Storage Access 授权存储。
 *
 * 授权键为 (嵌入站点, 顶层站点)：同一嵌入方在不同顶层站点下的授权互相隔离；
 * 顶层换站后原键天然查不到，且导航时会显式清理旧顶层站点下的全部授权。
 */
export class GrantStore {
  private readonly grants = new Map<Site, Map<Site, GrantRecord>>();

  grant(key: GrantKey, frameId: FrameId, at: number): void {
    let byTopLevel = this.grants.get(key.embeddedSite);
    if (!byTopLevel) {
      byTopLevel = new Map();
      this.grants.set(key.embeddedSite, byTopLevel);
    }
    byTopLevel.set(key.topLevelSite, { frameId, grantedAt: at });
  }

  has(key: GrantKey): boolean {
    return this.grants.get(key.embeddedSite)?.has(key.topLevelSite) ?? false;
  }

  /** 顶层站点变更：删除该顶层站点下所有嵌入方的授权。 */
  revokeForTopLevel(topLevelSite: Site): number {
    let removed = 0;
    for (const byTopLevel of this.grants.values()) {
      if (byTopLevel.delete(topLevelSite)) {
        removed += 1;
      }
    }
    return removed;
  }

  revokeForFrame(frameId: FrameId): void {
    for (const byTopLevel of this.grants.values()) {
      for (const [site, record] of byTopLevel) {
        if (record.frameId === frameId) {
          byTopLevel.delete(site);
        }
      }
    }
  }

  clear(): void {
    this.grants.clear();
  }
}

interface GrantRecord {
  frameId: FrameId;
  grantedAt: number;
}
