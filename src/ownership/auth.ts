import { verifyWithRawKey } from './crypto.ts';
import type { AuthSignature, KeyResolver, OwnershipEvent } from './types.ts';

export type AuthCheck =
  | { readonly ok: true }
  | { readonly ok: false; readonly reason: 'reject_bad_signature' | 'reject_transport_only' | 'wait_unknown_key'; readonly unknownKeyIds: readonly string[] };

/**
 * 验证信封上的授权签名。HTTP 传输签名不在此处出现，也永远不能替代授权签名。
 * - 没有任何授权签名：reject_transport_only（仅有传输来源证明）。
 * - 密钥尚未可达：wait_unknown_key（乱序到达，稍后可能补齐）。
 * - 密钥可达但密码学校验失败：reject_bad_signature（确定性拒绝，不转等待）。
 */
export function checkAuthorization(
  body: OwnershipEvent,
  auth: readonly AuthSignature[],
  resolver: KeyResolver,
): AuthCheck {
  if (!auth || auth.length === 0) {
    return { ok: false, reason: 'reject_transport_only', unknownKeyIds: [] };
  }
  const unknownKeyIds: string[] = [];
  for (const entry of auth) {
    const publicKey = resolver.resolveKey(entry.keyId);
    if (publicKey === null) {
      unknownKeyIds.push(entry.keyId);
      continue;
    }
    if (!verifyWithRawKey(publicKey, body, entry.signature)) {
      return { ok: false, reason: 'reject_bad_signature', unknownKeyIds: [] };
    }
  }
  if (unknownKeyIds.length > 0) {
    return { ok: false, reason: 'wait_unknown_key', unknownKeyIds };
  }
  return { ok: true };
}

/** 授权签名中是否包含指定 keyId 的有效条目（调用前应已通过 checkAuthorization）。 */
export function hasSigner(auth: readonly AuthSignature[], keyId: string): boolean {
  return auth.some((entry) => entry.keyId === keyId);
}
