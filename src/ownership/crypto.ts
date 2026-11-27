import {
  createPrivateKey,
  createPublicKey,
  generateKeyPairSync,
  sign,
  verify,
  type KeyObject,
} from 'node:crypto';
import { canonicalEventBytes, canonicalJson, sha256Base64Url } from './canonical.ts';
import type { KeyResolver, OwnershipEvent } from './types.ts';

/** 可序列化的 Ed25519 私钥（PKCS8 PEM）。 */
export interface MemberKey {
  readonly keyId: string;
  readonly publicKeyB64: string;
  readonly privateKeyPem: string;
}

export function keyIdFromPublic(publicKeyB64: string): string {
  return sha256Base64Url(Buffer.from(publicKeyB64, 'base64url'));
}

/** 生成一名成员的 Ed25519 密钥，keyId = sha256(原始公钥)。 */
export function generateMemberKey(): MemberKey {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  const publicKeyDer = publicKey.export({ format: 'der', type: 'spki' });
  const raw = publicKeyDer.subarray(publicKeyDer.length - 32);
  const publicKeyB64 = raw.toString('base64url');
  return {
    keyId: keyIdFromPublic(publicKeyB64),
    publicKeyB64,
    privateKeyPem: privateKey.export({ format: 'pem', type: 'pkcs8' }).toString(),
  };
}

export function loadPrivateKey(pem: string): KeyObject {
  return createPrivateKey(pem);
}

/** 对规范化事件体生成 base64url 授权签名。 */
export function signEvent(privateKey: KeyObject | string, body: OwnershipEvent): string {
  const key = typeof privateKey === 'string' ? loadPrivateKey(privateKey) : privateKey;
  return sign(null, canonicalEventBytes(body), key).toString('base64url');
}

/** 对任意规范化可序列化对象签名（用于恢复主张等非事件载荷）。 */
export function signCanonical(privateKey: KeyObject | string, payload: unknown): string {
  const key = typeof privateKey === 'string' ? loadPrivateKey(privateKey) : privateKey;
  return sign(null, Buffer.from(canonicalJson(payload), 'utf8'), key).toString('base64url');
}

/** 验证任意规范化对象的签名。 */
export function verifyCanonical(publicKeyB64: string, payload: unknown, signatureB64: string): boolean {
  try {
    const key = rawToPublicKey(publicKeyB64);
    return verify(
      null,
      Buffer.from(canonicalJson(payload), 'utf8'),
      key,
      Buffer.from(signatureB64, 'base64url'),
    );
  } catch {
    return false;
  }
}

const ED25519_SPKI_PREFIX = Buffer.from('302a300506032b6570032100', 'hex');

function rawToPublicKey(publicKeyB64: string): KeyObject {
  const spki = Buffer.concat([ED25519_SPKI_PREFIX, Buffer.from(publicKeyB64, 'base64url')]);
  return createPublicKey({ key: spki, format: 'der', type: 'spki' });
}

/** 用原始公钥（base64url）验证授权签名。 */
export function verifyWithRawKey(
  publicKeyB64: string,
  body: OwnershipEvent,
  signatureB64: string,
): boolean {
  try {
    const key = rawToPublicKey(publicKeyB64);
    return verify(null, canonicalEventBytes(body), key, Buffer.from(signatureB64, 'base64url'));
  } catch {
    return false;
  }
}

/**
 * 内存公钥注册表。只负责 keyId -> 公钥材料；身份等价（密钥别名/轮换）
 * 必须由事件日志中的 addKeyAlias 事件证明，注册表本身不做信任推断。
 */
export class InMemoryKeyRegistry implements KeyResolver {
  readonly #keys = new Map<string, string>();

  add(member: Pick<MemberKey, 'keyId' | 'publicKeyB64'>): this {
    if (this.#keys.has(member.keyId) && this.#keys.get(member.keyId) !== member.publicKeyB64) {
      throw new Error(`conflicting public key for ${member.keyId}`);
    }
    this.#keys.set(member.keyId, member.publicKeyB64);
    return this;
  }

  resolveKey(keyId: string): string | null {
    return this.#keys.get(keyId) ?? null;
  }

  get size(): number {
    return this.#keys.size;
  }
}

/** 任意字节的便捷签名（用于检查点、轮换证书等非事件载荷）。 */
export function signBytes(privateKey: KeyObject | string, payload: Buffer): string {
  const key = typeof privateKey === 'string' ? loadPrivateKey(privateKey) : privateKey;
  return sign(null, payload, key).toString('base64url');
}

export function verifyBytes(publicKeyB64: string, payload: Buffer, signatureB64: string): boolean {
  try {
    const key = rawToPublicKey(publicKeyB64);
    return verify(null, payload, key, Buffer.from(signatureB64, 'base64url'));
  } catch {
    return false;
  }
}
