/**
 * Ed25519 签名工具与“可签名消息”构造约定。
 *
 * 签名一律覆盖一个带用途标签的信封，防止跨事件类型的签名混用：
 *   { purpose: string, payload: <规范化后的负载，不含签名字段本身> }
 * 公钥以 SPKI DER 的 base64url 形式跨域交换。
 */

import { createPrivateKey, createPublicKey, generateKeyPairSync, sign, verify } from 'node:crypto';
import { canonicalBytes } from './canonical.ts';

export interface KeyPairMaterial {
  publicKeySpki: string;
  privateKeyPkcs8: string;
}

export interface SigningKey {
  keyId: string;
  privateKeyPkcs8: string;
  publicKeySpki: string;
}

/** 生成一把 Ed25519 密钥对。 */
export function generateEd25519KeyPair(): KeyPairMaterial {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  return {
    publicKeySpki: publicKey.export({ type: 'spki', format: 'der' }).toString('base64url'),
    privateKeyPkcs8: privateKey.export({ type: 'pkcs8', format: 'der' }).toString('base64url'),
  };
}

/** 生成带版本标识的签名密钥（测试与服务端引导使用）。 */
export function generateSigningKey(keyId: string): SigningKey {
  return { keyId, ...generateEd25519KeyPair() };
}

function loadPrivateKey(privateKeyPkcs8: string) {
  return createPrivateKey({
    key: Buffer.from(privateKeyPkcs8, 'base64url'),
    format: 'der',
    type: 'pkcs8',
  });
}

function loadPublicKey(publicKeySpki: string) {
  return createPublicKey({
    key: Buffer.from(publicKeySpki, 'base64url'),
    format: 'der',
    type: 'spki',
  });
}

/** 构造带用途标签的规范化消息字节。 */
export function signingMessage(purpose: string, payload: unknown): Buffer {
  return canonicalBytes({ purpose, payload });
}

/** 使用 PKCS#8 私钥对规范化消息签名，输出 base64url。 */
export function signMessage(privateKeyPkcs8: string, purpose: string, payload: unknown): string {
  return sign(null, signingMessage(purpose, payload), loadPrivateKey(privateKeyPkcs8)).toString(
    'base64url',
  );
}

/** 使用 SPKI 公钥验证 base64url 签名。 */
export function verifySignature(
  publicKeySpki: string,
  purpose: string,
  payload: unknown,
  signatureB64Url: string,
): boolean {
  let signature: Buffer;
  try {
    signature = Buffer.from(signatureB64Url, 'base64url');
  } catch {
    return false;
  }
  try {
    return verify(null, signingMessage(purpose, payload), loadPublicKey(publicKeySpki), signature);
  } catch {
    return false;
  }
}

/** 校验 SPKI 字符串是否可解析为 Ed25519 公钥。 */
export function isUsableEd25519PublicKey(publicKeySpki: string): boolean {
  try {
    const key = loadPublicKey(publicKeySpki);
    return key.asymmetricKeyType === 'ed25519';
  } catch {
    return false;
  }
}
