/**
 * Private State Token 兑换组件的领域类型。
 *
 * 设计目标：所有外部依赖（密码验证、存储、时钟、ID 生成）都以接口注入，
 * 组件本身只负责兑换策略与记账规则，不绑定具体密码库或数据库。
 */

/** 密钥承诺中的单个公钥条目。 */
export interface KeyCommitmentEntry {
  /** 密钥 ID，对应令牌签名所用的键。 */
  keyId: string;
  /** 公钥材料，语义由注入的 TokenVerifier 解释。 */
  publicKey: Uint8Array;
  /** 密钥过期时间（epoch 毫秒），到达该时间后该 keyId 不可再用于兑换。 */
  expiryMs: number;
  /** 可选的生效时间（epoch 毫秒），早于该时间密钥尚不可用。 */
  notBeforeMs?: number;
}

/** 发行方当前的密钥承诺与兑换策略。 */
export interface IssuerConfig {
  /** 发行方标识。 */
  issuerId: string;
  /** 当前密钥承诺：keyId -> 公钥条目。 */
  keys: Map<string, KeyCommitmentEntry>;
  /** 单次发放（一批兑换请求）允许的最大令牌数。 */
  batchSize: number;
  /** 兑换记录 / 去重记账的有效期（毫秒），由发行方配置决定。 */
  recordTtlMs: number;
}

/** 一枚待兑换的 Private State Token。 */
export interface PrivateStateToken {
  /** 令牌声称的发行方。 */
  issuerId: string;
  /** 签名所用密钥 ID，必须落在发行方当前密钥承诺内。 */
  keyId: string;
  /** 令牌序号，与（发行方、密钥 ID）共同构成唯一键。 */
  tokenIndex: number;
  /** 令牌上的密码学签名（及可选载荷），由注入的验证器校验。 */
  signature: Uint8Array;
  /** 可选的令牌载荷，验证器需要时使用。 */
  payload?: Uint8Array;
}

/** 兑换请求。 */
export interface RedemptionRequest {
  /** 发起兑换的顶层站点（eTLD+1），用于站点维度的发行方数量限制。 */
  topLevelSite: string;
  /** 本批次一次性提交兑换的令牌。 */
  tokens: PrivateStateToken[];
}

/** 兑换成功后生成的兑换记录（Redemption Record）。 */
export interface RedemptionRecord {
  /** 兑换记录 ID，由注入的 idFactory 生成。 */
  recordId: string;
  issuerId: string;
  topLevelSite: string;
  /** 本记录覆盖的全部令牌唯一键。 */
  tokenKeys: RedemptionKey[];
  redeemedAtMs: number;
  /** redeemedAtMs + 发行方配置的 recordTtlMs。 */
  expiresAtMs: number;
}

/** 令牌去重唯一键：（发行方、密钥 ID、令牌序号）。 */
export interface RedemptionKey {
  issuerId: string;
  keyId: string;
  tokenIndex: number;
}

/** 兑换成功的返回值。 */
export interface RedemptionResult {
  record: RedemptionRecord;
  /** 本次实际记账的令牌键。 */
  redeemedKeys: RedemptionKey[];
}

/** 兑换记录消费（出示）的状态。 */
export type RedemptionConsumeStatus =
  | 'ok'
  | 'already-presented'
  | 'expired'
  | 'not-found';

/** 发行方配置目录。 */
export interface IssuerDirectory {
  getIssuerConfig(issuerId: string): Promise<IssuerConfig | null>;
}

/**
 * 令牌去重存储。
 *
 * 实现必须保证 claim 的原子性：并发调用同一 key 时，至多一个调用返回 true。
 * 已过期的条目允许被新的 claim 覆盖（同一令牌在 TTL 过后可重新兑换）。
 */
export interface TokenRedemptionStore {
  /**
   * 原子占用一个令牌键。
   * @returns true 表示首次占用成功；false 表示存在未过期的占用（重复兑换）。
   */
  claim(key: RedemptionKey, expiresAtMs: number, nowMs: number): Promise<boolean>;
  /** 释放本批次内已占用的键，用于批次中途失败时回滚。 */
  release(key: RedemptionKey): Promise<void>;
}

/** 兑换记录存储。 */
export interface RedemptionRecordStore {
  save(record: RedemptionRecord): Promise<void>;
  findById(recordId: string): Promise<RedemptionRecord | null>;
  /**
   * 原子消费一条兑换记录（出示）。
   * 同一记录在有效期内只能成功消费一次，重复出示返回 already-presented。
   */
  consume(recordId: string, nowMs: number): Promise<RedemptionConsumeStatus>;
}

/**
 * 顶层站点维度的“同时有效发行方”记账存储。
 * 每个顶层站点同时有效（未过期）的发行方数量不得超过 2。
 */
export interface ActiveIssuerStore {
  /** 刷新（或新建）某站点下某发行方的有效期。 */
  touch(site: string, issuerId: string, expiresAtMs: number, nowMs: number): Promise<void>;
  /** 该站点当前未过期的发行方数量。 */
  activeCount(site: string, nowMs: number): Promise<number>;
  /** 该发行方当前是否在该站点的有效集合内。 */
  isActive(site: string, issuerId: string, nowMs: number): Promise<boolean>;
}
