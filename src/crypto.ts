/**
 * 哈希与签名适配层。默认基于 Web Crypto（Node 全局 crypto），
 * 宿主可注入自定义 HashFn 以接入自身密码学实现。
 */

import { createHash, generateKeyPairSync, sign as nodeSign, verify as nodeVerify } from 'node:crypto';
import type { HashFn } from './merkle.ts';

export const sha256: HashFn = (data: Uint8Array): Uint8Array =>
  new Uint8Array(createHash('sha256').update(data).digest());

export interface KeyPairPem {
  readonly publicKeySpkiDer: Uint8Array;
  readonly privateKeyPkcs8Pem: string;
  readonly publicKeySpkiPem: string;
}

/** 生成测试用 Ed25519 密钥对（生产密钥应由带外渠道下发）。 */
export function generateEd25519TestKey(): KeyPairPem {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  return {
    publicKeySpkiDer: publicKey.export({ type: 'spki', format: 'der' }) as Uint8Array,
    publicKeySpkiPem: publicKey.export({ type: 'spki', format: 'pem' }) as string,
    privateKeyPkcs8Pem: privateKey.export({ type: 'pkcs8', format: 'pem' }) as string,
  };
}

export function signDetached(privateKeyPkcs8Pem: string, message: Uint8Array): Uint8Array {
  return new Uint8Array(nodeSign(null, message, privateKeyPkcs8Pem));
}

export function verifyDetached(publicKeySpkiDer: Uint8Array, message: Uint8Array, signature: Uint8Array): boolean {
  return nodeVerify(null, message, { key: Buffer.from(publicKeySpkiDer), format: 'der', type: 'spki' }, signature);
}
