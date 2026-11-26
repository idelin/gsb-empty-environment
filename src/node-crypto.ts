// @ts-ignore -- `node:crypto` is available in Node; consumers with @types/node get full types.
import { createHmac, timingSafeEqual } from 'node:crypto';
import { moduleSigningPayload } from './canonical.ts';
import type { SignatureVerifier, SignedWorkModule } from './types.ts';

export function signModuleHmac(module: SignedWorkModule, secret: string): string {
  return createHmac('sha256', secret).update(moduleSigningPayload(module), 'utf8').digest('hex');
}

function hexToBytes(hex: string): Uint8Array {
  if (!/^[0-9a-fA-F]*$/.test(hex) || hex.length % 2 !== 0) return new Uint8Array(0);
  const bytes = new Uint8Array(hex.length / 2);
  for (let index = 0; index < bytes.length; index += 1) {
    bytes[index] = Number.parseInt(hex.slice(index * 2, index * 2 + 2), 16);
  }
  return bytes;
}

export function createHmacSignatureVerifier(secret: string): SignatureVerifier {
  return ({ module, signature }): boolean => {
    const expected = signModuleHmac(module, secret);
    const actualBytes = hexToBytes(signature);
    const expectedBytes = hexToBytes(expected);
    if (actualBytes.length !== expectedBytes.length || actualBytes.length === 0) return false;
    try {
      return timingSafeEqual(actualBytes, expectedBytes);
    } catch {
      return false;
    }
  };
}
