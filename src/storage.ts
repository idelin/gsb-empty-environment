import type {
  ActiveIssuerStore,
  IssuerConfig,
  IssuerDirectory,
  RedemptionConsumeStatus,
  RedemptionKey,
  RedemptionRecord,
  RedemptionRecordStore,
  TokenRedemptionStore,
} from './types.ts';

/** 将令牌唯一键序列化为稳定字符串。 */
export function encodeTokenKey(key: RedemptionKey): string {
  return `${key.issuerId}\u0000${key.keyId}\u0000${key.tokenIndex}`;
}

/** 内存版发行方目录。 */
export class InMemoryIssuerDirectory implements IssuerDirectory {
  private readonly configs = new Map<string, IssuerConfig>();

  constructor(configs: Iterable<IssuerConfig> = []) {
    for (const config of configs) {
      this.configs.set(config.issuerId, config);
    }
  }

  upsert(config: IssuerConfig): this {
    this.configs.set(config.issuerId, config);
    return this;
  }

  async getIssuerConfig(issuerId: string): Promise<IssuerConfig | null> {
    return this.configs.get(issuerId) ?? null;
  }
}

interface ClaimEntry {
  expiresAtMs: number;
}

/**
 * 内存版令牌去重存储。
 *
 * JS 事件循环内 Map 读写为原子操作；claim 先判定再写入的序列不会被重入打断，
 * 因此同一 key 的并发 claim 至多一个成功。真实数据库实现应使用带 TTL 的
 * upsert（如 SET NX PX）保证同样的原子语义。
 */
export class InMemoryTokenRedemptionStore implements TokenRedemptionStore {
  private readonly claims = new Map<string, ClaimEntry>();

  async claim(key: RedemptionKey, expiresAtMs: number, nowMs: number): Promise<boolean> {
    const id = encodeTokenKey(key);
    const existing = this.claims.get(id);
    if (existing && existing.expiresAtMs > nowMs) {
      return false;
    }
    this.claims.set(id, { expiresAtMs });
    return true;
  }

  async release(key: RedemptionKey): Promise<void> {
    this.claims.delete(encodeTokenKey(key));
  }

  /** 测试辅助：当前未过期的占用数量。 */
  activeSize(nowMs: number): number {
    let n = 0;
    for (const entry of this.claims.values()) {
      if (entry.expiresAtMs > nowMs) {
        n += 1;
      }
    }
    return n;
  }
}

interface StoredRecord {
  record: RedemptionRecord;
  presented: boolean;
}

/** 内存版兑换记录存储。 */
export class InMemoryRedemptionRecordStore implements RedemptionRecordStore {
  private readonly records = new Map<string, StoredRecord>();

  async save(record: RedemptionRecord): Promise<void> {
    this.records.set(record.recordId, { record, presented: false });
  }

  async findById(recordId: string): Promise<RedemptionRecord | null> {
    return this.records.get(recordId)?.record ?? null;
  }

  async consume(recordId: string, nowMs: number): Promise<RedemptionConsumeStatus> {
    const stored = this.records.get(recordId);
    if (!stored) {
      return 'not-found';
    }
    if (stored.record.expiresAtMs <= nowMs) {
      return 'expired';
    }
    if (stored.presented) {
      return 'already-presented';
    }
    stored.presented = true;
    return 'ok';
  }

  /** 测试辅助：记录是否已被出示。 */
  isPresented(recordId: string): boolean {
    return this.records.get(recordId)?.presented ?? false;
  }
}

/** 内存版站点活跃发行方存储，所有读取都会先惰性清理过期条目。 */
export class InMemoryActiveIssuerStore implements ActiveIssuerStore {
  private readonly bySite = new Map<string, Map<string, number>>();

  async touch(
    site: string,
    issuerId: string,
    expiresAtMs: number,
    nowMs: number,
  ): Promise<void> {
    const issuers = this.siteMap(site);
    this.prune(issuers, nowMs);
    issuers.set(issuerId, expiresAtMs);
  }

  async activeCount(site: string, nowMs: number): Promise<number> {
    const issuers = this.siteMap(site);
    this.prune(issuers, nowMs);
    return issuers.size;
  }

  async isActive(site: string, issuerId: string, nowMs: number): Promise<boolean> {
    const issuers = this.siteMap(site);
    this.prune(issuers, nowMs);
    return issuers.has(issuerId);
  }

  private siteMap(site: string): Map<string, number> {
    let issuers = this.bySite.get(site);
    if (!issuers) {
      issuers = new Map();
      this.bySite.set(site, issuers);
    }
    return issuers;
  }

  private prune(issuers: Map<string, number>, nowMs: number): void {
    for (const [issuerId, expiresAtMs] of issuers) {
      if (expiresAtMs <= nowMs) {
        issuers.delete(issuerId);
      }
    }
  }
}
