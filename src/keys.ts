import { createPrivateKey, createPublicKey, generateKeyPairSync, sign, verify } from 'node:crypto';
import { sha256 } from './encoding.ts';

export interface KeyPair {
  publicDer: Uint8Array;
  privateDer: Uint8Array;
}

export function generateKey(): KeyPair {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  return {
    publicDer: new Uint8Array(publicKey.export({ type: 'spki', format: 'der' })),
    privateDer: new Uint8Array(privateKey.export({ type: 'pkcs8', format: 'der' })),
  };
}

export function keyIdOf(publicDer: Uint8Array): string {
  return sha256(publicDer);
}

export function signBytes(privateDer: Uint8Array, msg: Uint8Array): Uint8Array {
  const key = createPrivateKey({ key: Buffer.from(privateDer), format: 'der', type: 'pkcs8' });
  return new Uint8Array(sign(null, Buffer.from(msg), key));
}

export function verifyBytes(publicDer: Uint8Array, msg: Uint8Array, sig: Uint8Array): boolean {
  try {
    const key = createPublicKey({ key: Buffer.from(publicDer), format: 'der', type: 'spki' });
    return verify(null, Buffer.from(msg), key, Buffer.from(sig));
  } catch {
    return false;
  }
}
