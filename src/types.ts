/**
 * 登记表中单个 kid 的密钥来源记录。
 *
 * - `publicJwk`：调用方登记并唯一信任的公钥（JWK）。令牌头里的 jwk/x5c
 *   永远不会取代它。
 * - `allowedAlgs`：该来源允许的 JWS `alg` 集合。
 * - `allowedHost` / `allowedPathPrefix`：jku / x5u 允许指向的来源。
 *   主机比较不分大小写，路径逐字比较，并要求前缀落在路径段边界上。
 */
export interface RegisteredKeyEntry {
  kid: string;
  publicJwk: JsonWebKey;
  allowedAlgs: ReadonlyArray<string>;
  allowedHost: string;
  allowedPathPrefix: string;
}

/** 调用方提供的 kid -> 密钥来源登记表。 */
export type KeyRegistry = Readonly<Record<string, RegisteredKeyEntry>>;

/** 验签前需要判定的 JWS 受保护头（只读取本包关心的字段）。 */
export interface JwsHeaderLike {
  kid?: unknown;
  alg?: unknown;
  jwk?: unknown;
  x5c?: unknown;
  jku?: unknown;
  x5u?: unknown;
  [parameter: string]: unknown;
}

/** 拒绝原因，全部属于安全拒绝，调用方应按伪造/非法令牌处理（4xx，不重试）。 */
export type RejectReason =
  | 'missing_kid'
  | 'unknown_kid'
  | 'missing_alg'
  | 'alg_not_allowed'
  | 'forged_jwk'
  | 'invalid_header_jwk'
  | 'multiple_key_urls'
  | 'invalid_jku'
  | 'invalid_x5u';

export interface KeySourceRejection {
  kind: 'reject';
  reason: RejectReason;
  detail: string;
}

/** 直接使用登记表里的公钥，无需任何网络抓取。 */
export interface UseRegisteredKeyDecision {
  kind: 'use-registered-key';
  kid: string;
  alg: string;
  /** 登记表中的公钥 JWK；令牌自带的 jwk/x5c 已被忽略。 */
  publicJwk: JsonWebKey;
  entry: RegisteredKeyEntry;
}

/** URL 通过来源策略，调用方可随后用 {@link fetchKeyMaterial} 安全抓取。 */
export interface FetchKeyUrlDecision {
  kind: 'fetch-url';
  kid: string;
  alg: string;
  /** 来源头字段：jku（JWK Set）或 x5u（X.509 证书/链）。 */
  header: 'jku' | 'x5u';
  /** 已规范化但逐字保留路径的 https URL。 */
  url: string;
  entry: RegisteredKeyEntry;
}

export type KeySourceDecision =
  | KeySourceRejection
  | UseRegisteredKeyDecision
  | FetchKeyUrlDecision;

/** 抓取返回的密钥材料。 */
export interface FetchedKeyMaterial {
  url: string;
  contentType: string | null;
  /** 最多 64 KiB 的原始响应体。 */
  bytes: Uint8Array;
}

export interface JwkSet {
  keys: JsonWebKey[];
}
