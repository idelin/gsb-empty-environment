import { canonicalJson, sha256Base64Url } from './canonical.ts';
import { verifyBytes, verifyWithRawKey } from './crypto.ts';
import type { Envelope, KeyResolver, OwnershipEvent } from './types.ts';

/**
 * 历史授权证书：把某事件与当时的公钥绑定，证明"在该时点该密钥有权签署"。
 * 即使后续发生密钥轮换（addKeyAlias），历史证书仍可独立验证。
 */
export interface AuthorizationCertificate {
  readonly eventHash: string;
  readonly keyId: string;
  readonly publicKeyB64: string;
  readonly issuedAt: number;
  /** 签发者（受信历史存档方）对证书载荷的签名。 */
  readonly issuerSignature: string;
  /** 可选：指向更早证书，形成历史链。 */
  readonly prevCertHash: string | null;
}

export function certificatePayload(cert: Omit<AuthorizationCertificate, 'issuerSignature'>): Buffer {
  return Buffer.from(canonicalJson(cert), 'utf8');
}

export function certificateHash(cert: AuthorizationCertificate): string {
  return sha256Base64Url(canonicalJson(cert));
}

/** 用存档签发者公钥验证证书本身。 */
export function verifyCertificate(
  cert: AuthorizationCertificate,
  issuerPublicKeyB64: string,
): boolean {
  const { issuerSignature, ...unsigned } = cert;
  return verifyBytes(issuerPublicKeyB64, certificatePayload(unsigned), issuerSignature);
}

export interface HistoricalVerification {
  readonly valid: boolean;
  readonly reason:
    | 'ok'
    | 'cert_signature_invalid'
    | 'key_mismatch'
    | 'event_mismatch'
    | 'auth_signature_invalid';
}

/**
 * 历史授权验证：给定完整事件信封、历史证书与密钥解析器，
 * 证明某授权签名在历史上成立。验证不写缓存、不修改解析器。
 */
export function verifyHistoricalAuthorization(
  envelope: Envelope,
  cert: AuthorizationCertificate,
  issuerPublicKeyB64: string,
  resolver: KeyResolver,
): HistoricalVerification {
  if (!verifyCertificate(cert, issuerPublicKeyB64)) {
    return { valid: false, reason: 'cert_signature_invalid' };
  }
  if (cert.eventHash !== envelope.eventHash) {
    return { valid: false, reason: 'event_mismatch' };
  }
  const currentKey = resolver.resolveKey(cert.keyId);
  if (currentKey !== null && currentKey !== cert.publicKeyB64) {
    return { valid: false, reason: 'key_mismatch' };
  }
  const authEntry = envelope.auth.find((entry) => entry.keyId === cert.keyId);
  if (!authEntry) {
    return { valid: false, reason: 'auth_signature_invalid' };
  }
  const body: OwnershipEvent = envelope.body;
  if (!verifyWithRawKey(cert.publicKeyB64, body, authEntry.signature)) {
    return { valid: false, reason: 'auth_signature_invalid' };
  }
  return { valid: true, reason: 'ok' };
}
