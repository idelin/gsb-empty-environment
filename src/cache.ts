import { serializeOrigin } from "./origin.ts";
import { serializeKey } from "./matrix.ts";
import type { AdmissionInput, CacheEntry, PolicyKey } from "./types.ts";

/**
 * 从一次获许访问构造缓存键。
 *
 * 键中包含完整安全上下文：
 * 资源 URL、文档来源（scheme/host/port）、资源模式、凭据模式、
 * 嵌入策略、来源关系以及资源声明（CORP、CORS 许可）。
 * 任一属性变化都会得到不同的键，因此不同安全上下文之间
 * 不可能命中同一条缓存响应。
 */
export function buildCacheKey(
  resourceUrl: string,
  documentOrigin: AdmissionInput["documentOrigin"],
  key: PolicyKey,
): string {
  return JSON.stringify({
    u: resourceUrl,
    d: serializeOrigin(documentOrigin),
    k: serializeKey(key),
  });
}

/**
 * 安全上下文绑定缓存。
 *
 * 仅存储被矩阵判定为 allow 的响应；deny 不产生缓存条目，
 * 从而杜绝“先在宽松上下文获许、再在隔离上下文复用”的路径。
 */
export class SecurityContextCache {
  private readonly store = new Map<string, CacheEntry>();

  /**
   * 存入一条获许响应。verdict 非 allow 时拒绝写入并返回 false。
   */
  put(entry: CacheEntry): boolean {
    if (entry.verdict !== "allow") {
      return false;
    }
    const key = buildCacheKey(
      entry.resourceUrl,
      entry.documentOrigin,
      {
        relationship: entry.relationship,
        resourceMode: entry.resourceMode,
        credentials: entry.credentials,
        embedding: entry.embedding,
        corp: entry.corp,
        corsAccess: entry.corsAccess,
      },
    );
    this.store.set(key, { ...entry });
    return true;
  }

  /**
   * 按完整安全上下文查找缓存。
   * 上下文不完全一致（哪怕只改一个维度）都返回 undefined。
   */
  get(
    resourceUrl: string,
    documentOrigin: AdmissionInput["documentOrigin"],
    key: PolicyKey,
  ): CacheEntry | undefined {
    const cacheKey = buildCacheKey(resourceUrl, documentOrigin, key);
    const hit = this.store.get(cacheKey);
    return hit === undefined ? undefined : { ...hit };
  }

  /** 仅用于测试与诊断：当前缓存条目数。 */
  get size(): number {
    return this.store.size;
  }

  clear(): void {
    this.store.clear();
  }
}
