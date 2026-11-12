import { bytesToBase64Url, base64UrlToBytes, timingSafeEqualHex } from './encoding.ts';
import type { TenantCredentials } from './protocol.ts';

const HMAC_ALGORITHM = { name: 'HMAC', hash: 'SHA-256' } as const;

export interface TokenClaims {
  tenantId: string;
  scriptUrl: string;
  nonce: string;
  issuedAt: number;
  expiresAt: number;
}

export interface IssueTokenOptions {
  /** 注入随机数来源，测试可替换为确定性实现。 */
  nonce?: string;
  /** 注入当前时间（毫秒），测试可替换。 */
  now?: number;
  /** 令牌有效期（毫秒），默认 60 秒。 */
  ttlMs?: number;
}

export type TokenRejectReason =
  | 'token_rejected'
  | 'token_expired'
  | 'token_replayed';

export interface TokenIssue {
  token: string;
  nonce: string;
  expiresAt: number;
}

async function importSecret(secret: string): Promise<CryptoKey> {
  return crypto.subtle.importKey(
    'raw',
    new TextEncoder().encode(secret),
    HMAC_ALGORITHM,
    false,
    ['sign', 'verify'],
  );
}

function canonicalClaims(claims: TokenClaims): string {
  return [
    claims.tenantId,
    claims.scriptUrl,
    claims.nonce,
    String(claims.issuedAt),
    String(claims.expiresAt),
  ].join('\n');
}

export async function randomNonce(byteLength = 18): Promise<string> {
  const bytes = new Uint8Array(byteLength);
  crypto.getRandomValues(bytes);
  return bytesToBase64Url(bytes);
}

/**
 * 签发绑定租户与脚本 URL 的握手令牌。
 * 令牌内含一次性 nonce，worker 侧通过 nonce 去重实现防重放。
 */
export async function issueHandshakeToken(
  credentials: TenantCredentials,
  scriptUrl: string,
  options: IssueTokenOptions = {},
): Promise<TokenIssue> {
  const now = options.now ?? Date.now();
  const ttlMs = options.ttlMs ?? 60_000;
  const nonce = options.nonce ?? (await randomNonce());
  const claims: TokenClaims = {
    tenantId: credentials.tenantId,
    scriptUrl,
    nonce,
    issuedAt: now,
    expiresAt: now + ttlMs,
  };
  const key = await importSecret(credentials.secret);
  const signatureBuffer = await crypto.subtle.sign(
    HMAC_ALGORITHM.name,
    key,
    new TextEncoder().encode(canonicalClaims(claims)),
  );
  const signature = bytesToBase64Url(new Uint8Array(signatureBuffer));
  const payload = bytesToBase64Url(
    new TextEncoder().encode(JSON.stringify(claims)),
  );
  return {
    token: `${payload}.${signature}`,
    nonce,
    expiresAt: claims.expiresAt,
  };
}

export interface VerifyTokenOptions {
  now?: number;
  /** nonce 重放判定：返回 true 表示已使用。 */
  isReplayed: (nonce: string) => boolean;
  /** 校验通过后登记 nonce，使同一令牌无法再次使用。 */
  rememberNonce: (nonce: string, expiresAt: number) => void;
}

export type TokenVerifyResult =
  | { ok: true; claims: TokenClaims }
  | { ok: false; reason: TokenRejectReason };

export async function verifyHandshakeToken(
  token: string,
  secret: string,
  options: VerifyTokenOptions,
): Promise<TokenVerifyResult> {
  const separatorIndex = token.indexOf('.');
  if (separatorIndex <= 0 || separatorIndex === token.length - 1) {
    return { ok: false, reason: 'token_rejected' };
  }
  const payload = token.slice(0, separatorIndex);
  const providedSignature = token.slice(separatorIndex + 1);

  let claims: TokenClaims;
  try {
    claims = JSON.parse(
      new TextDecoder().decode(base64UrlToBytes(payload)),
    ) as TokenClaims;
  } catch {
    return { ok: false, reason: 'token_rejected' };
  }

  if (
    typeof claims.tenantId !== 'string' ||
    typeof claims.scriptUrl !== 'string' ||
    typeof claims.nonce !== 'string' ||
    typeof claims.issuedAt !== 'number' ||
    typeof claims.expiresAt !== 'number'
  ) {
    return { ok: false, reason: 'token_rejected' };
  }

  const key = await importSecret(secret);
  const expectedSignatureBuffer = await crypto.subtle.sign(
    HMAC_ALGORITHM.name,
    key,
    new TextEncoder().encode(canonicalClaims(claims)),
  );
  const expectedSignature = bytesToBase64Url(
    new Uint8Array(expectedSignatureBuffer),
  );
  if (!timingSafeEqualHex(providedSignature, expectedSignature)) {
    return { ok: false, reason: 'token_rejected' };
  }

  const now = options.now ?? Date.now();
  if (claims.expiresAt <= now || claims.issuedAt > now + 60_000) {
    return { ok: false, reason: 'token_expired' };
  }

  if (options.isReplayed(claims.nonce)) {
    return { ok: false, reason: 'token_replayed' };
  }
  options.rememberNonce(claims.nonce, claims.expiresAt);
  return { ok: true, claims };
}
