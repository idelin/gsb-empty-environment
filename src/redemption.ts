import { RedemptionError, duplicateTokenError } from './errors.ts';
import {
  InMemoryActiveIssuerStore,
  InMemoryRedemptionRecordStore,
  InMemoryTokenRedemptionStore,
  encodeTokenKey,
} from './storage.ts';
import type {
  ActiveIssuerStore,
  IssuerConfig,
  IssuerDirectory,
  PrivateStateToken,
  RedemptionKey,
  RedemptionRecord,
  RedemptionRecordStore,
  RedemptionRequest,
  RedemptionResult,
  TokenRedemptionStore,
} from './types.ts';
import type { TokenVerifier } from './crypto.ts';

/** 每个顶层站点允许同时有效的发行方上限（规范常量）。 */
export const DEFAULT_MAX_ACTIVE_ISSUERS_PER_SITE = 2;

export interface RedemptionServiceOptions {
  /** 密码验证器（必选注入）。 */
  verifier: TokenVerifier;
  /** 发行方配置目录（必选注入）。 */
  issuerDirectory: IssuerDirectory;
  tokenStore?: TokenRedemptionStore;
  recordStore?: RedemptionRecordStore;
  activeIssuerStore?: ActiveIssuerStore;
  /** 时钟注入，默认 Date.now；测试可注入可控时钟。 */
  clock?: () => number;
  /** 兑换记录 ID 工厂，默认随机 UUID。 */
  idFactory?: () => string;
  /** 若本服务只服务单一发行方，可固定 issuerId，跨发行方令牌直接拒绝。 */
  fixedIssuerId?: string;
  /** 每站点同时有效发行方上限，默认 2。 */
  maxActiveIssuersPerSite?: number;
}

/** 简单的进程内互斥锁，保证异步验证 + 记账序列的并发正确性。 */
class AsyncMutex {
  private chain: Promise<void> = Promise.resolve();

  async run<T>(task: () => Promise<T>): Promise<T> {
    const previous = this.chain;
    let release: () => void;
    this.chain = new Promise<void>((resolve) => {
      release = resolve;
    });
    await previous;
    try {
      return await task();
    } finally {
      release!();
    }
  }
}

/**
 * Private State Token 兑换服务。
 *
 * 规则：
 *  1. 令牌按（发行方、密钥 ID、令牌序号）在发行方配置的 TTL 内只能兑换一次；
 *  2. keyId 必须在该发行方当前密钥承诺内，且未过期、已生效；
 *  3. 单批令牌数不得超过承诺的 batchSize；
 *  4. 兑换记录 TTL 取发行方配置，记录在有效期内不能被再次出示；
 *  5. 每个顶层站点同时有效的发行方不超过 2 个。
 */
export class RedemptionService {
  private readonly verifier: TokenVerifier;
  private readonly issuerDirectory: IssuerDirectory;
  private readonly tokenStore: TokenRedemptionStore;
  private readonly recordStore: RedemptionRecordStore;
  private readonly activeIssuerStore: ActiveIssuerStore;
  private readonly clock: () => number;
  private readonly idFactory: () => string;
  private readonly fixedIssuerId?: string;
  private readonly maxActiveIssuersPerSite: number;
  private readonly mutex = new AsyncMutex();

  constructor(options: RedemptionServiceOptions) {
    this.verifier = options.verifier;
    this.issuerDirectory = options.issuerDirectory;
    this.tokenStore = options.tokenStore ?? new InMemoryTokenRedemptionStore();
    this.recordStore = options.recordStore ?? new InMemoryRedemptionRecordStore();
    this.activeIssuerStore =
      options.activeIssuerStore ?? new InMemoryActiveIssuerStore();
    this.clock = options.clock ?? (() => Date.now());
    this.idFactory = options.idFactory ?? (() => crypto.randomUUID());
    this.fixedIssuerId = options.fixedIssuerId;
    this.maxActiveIssuersPerSite =
      options.maxActiveIssuersPerSite ?? DEFAULT_MAX_ACTIVE_ISSUERS_PER_SITE;
  }

  /** 处理一批令牌的兑换请求，成功返回兑换记录。 */
  redeem(request: RedemptionRequest): Promise<RedemptionResult> {
    return this.mutex.run(() => this.redeemLocked(request));
  }

  /** 出示一条兑换记录；有效期内重复出示会被拒绝。 */
  async presentRecord(recordId: string): Promise<RedemptionRecord> {
    const now = this.clock();
    const status = await this.recordStore.consume(recordId, now);
    switch (status) {
      case 'ok':
        return (await this.recordStore.findById(recordId))!;
      case 'not-found':
        throw new RedemptionError('RECORD_NOT_FOUND', '兑换记录不存在', { recordId });
      case 'expired':
        throw new RedemptionError('RECORD_EXPIRED', '兑换记录已过期', { recordId });
      case 'already-presented':
        throw new RedemptionError(
          'RECORD_ALREADY_PRESENTED',
          '兑换记录已被出示，不能再次使用',
          { recordId },
        );
      default: {
        const exhaustive: never = status;
        throw new Error(`未预期的记录状态：${String(exhaustive)}`);
      }
    }
  }

  private async redeemLocked(request: RedemptionRequest): Promise<RedemptionResult> {
    const now = this.clock();
    const { tokens, topLevelSite } = request;

    if (tokens.length === 0) {
      throw new RedemptionError('EMPTY_BATCH', '兑换批次不能为空');
    }

    const issuerId = this.resolveIssuerId(tokens);
    const config = await this.issuerDirectory.getIssuerConfig(issuerId);
    if (!config) {
      throw new RedemptionError('UNKNOWN_ISSUER', `未知发行方：${issuerId}`, { issuerId });
    }

    if (tokens.length > config.batchSize) {
      throw new RedemptionError(
        'BATCH_TOO_LARGE',
        `批次令牌数 ${tokens.length} 超过承诺上限 ${config.batchSize}`,
        { batchSize: config.batchSize, actual: tokens.length },
      );
    }

    await this.validateTokens(tokens, config, now);
    await this.enforceActiveIssuerLimit(topLevelSite, issuerId, now);

    const keys = tokens.map(toRedemptionKey);
    const claimed: RedemptionKey[] = [];
    const expiresAtMs = now + config.recordTtlMs;

    for (const key of keys) {
      const acquired = await this.tokenStore.claim(key, expiresAtMs, now);
      if (!acquired) {
        await this.rollback(claimed);
        throw duplicateTokenError(key);
      }
      claimed.push(key);
    }

    const record: RedemptionRecord = {
      recordId: this.idFactory(),
      issuerId,
      topLevelSite,
      tokenKeys: keys,
      redeemedAtMs: now,
      expiresAtMs,
    };

    try {
      await this.recordStore.save(record);
      await this.activeIssuerStore.touch(topLevelSite, issuerId, expiresAtMs, now);
    } catch (cause) {
      await this.rollback(claimed);
      throw cause;
    }

    return { record, redeemedKeys: keys };
  }

  /** 校验批次内发行方一致（并满足可选固定发行方）。 */
  private resolveIssuerId(tokens: PrivateStateToken[]): string {
    const issuerId = this.fixedIssuerId ?? tokens[0].issuerId;
    for (const token of tokens) {
      if (token.issuerId !== issuerId) {
        throw new RedemptionError(
          'HETEROGENEOUS_ISSUER',
          '同一兑换批次中的令牌必须来自同一发行方',
          { expectedIssuer: issuerId, actualIssuer: token.issuerId },
        );
      }
      if (this.fixedIssuerId && token.issuerId !== this.fixedIssuerId) {
        throw new RedemptionError(
          'HETEROGENEOUS_ISSUER',
          `本服务只接受发行方 ${this.fixedIssuerId} 的令牌`,
          { expectedIssuer: this.fixedIssuerId, actualIssuer: token.issuerId },
        );
      }
    }
    return issuerId;
  }

  /** 逐枚校验密钥承诺、有效期与签名，并拒绝批次内重复令牌。 */
  private async validateTokens(
    tokens: PrivateStateToken[],
    config: IssuerConfig,
    now: number,
  ): Promise<void> {
    const seen = new Set<string>();
    for (const token of tokens) {
      const keyEntry = config.keys.get(token.keyId);
      if (!keyEntry) {
        throw new RedemptionError(
          'UNKNOWN_KEY',
          `密钥 ID ${token.keyId} 不在发行方 ${config.issuerId} 的当前承诺内`,
          { issuerId: config.issuerId, keyId: token.keyId },
        );
      }
      if (keyEntry.notBeforeMs !== undefined && now < keyEntry.notBeforeMs) {
        throw new RedemptionError('KEY_NOT_YET_VALID', '密钥尚未生效', {
          keyId: token.keyId,
          notBeforeMs: keyEntry.notBeforeMs,
        });
      }
      if (now >= keyEntry.expiryMs) {
        throw new RedemptionError('KEY_EXPIRED', `密钥 ${token.keyId} 已过期`, {
          keyId: token.keyId,
          expiryMs: keyEntry.expiryMs,
        });
      }

      const outcome = await this.verifier.verify(token, keyEntry);
      if (!outcome.valid) {
        throw new RedemptionError('INVALID_SIGNATURE', '令牌密码验证失败', {
          keyId: token.keyId,
          reason: outcome.reason,
        });
      }

      const key = toRedemptionKey(token);
      const encoded = encodeTokenKey(key);
      if (seen.has(encoded)) {
        throw duplicateTokenError(key);
      }
      seen.add(encoded);
    }
  }

  /** 站点维度发行方数量上限：已在集合内则放行，否则要求集合未满。 */
  private async enforceActiveIssuerLimit(
    site: string,
    issuerId: string,
    now: number,
  ): Promise<void> {
    const alreadyActive = await this.activeIssuerStore.isActive(site, issuerId, now);
    if (alreadyActive) {
      return;
    }
    const count = await this.activeIssuerStore.activeCount(site, now);
    if (count >= this.maxActiveIssuersPerSite) {
      throw new RedemptionError(
        'TOO_MANY_ACTIVE_ISSUERS',
        `站点 ${site} 同时有效的发行方已达上限 ${this.maxActiveIssuersPerSite}`,
        { site, active: count, limit: this.maxActiveIssuersPerSite },
      );
    }
  }

  private async rollback(keys: RedemptionKey[]): Promise<void> {
    for (const key of keys) {
      await this.tokenStore.release(key);
    }
  }
}

function toRedemptionKey(token: PrivateStateToken): RedemptionKey {
  return {
    issuerId: token.issuerId,
    keyId: token.keyId,
    tokenIndex: token.tokenIndex,
  };
}
