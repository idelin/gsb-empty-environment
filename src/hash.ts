import { createHash } from 'node:crypto';

export const GENESIS_HASH = '0'.repeat(64);

export function sha256(data: Buffer | string): string {
  return createHash('sha256').update(data).digest('hex');
}

/** 哈希链：H(i) = sha256(H(i-1) || data) */
export function chainHash(prevHash: string, data: Buffer): string {
  return createHash('sha256')
    .update(Buffer.from(prevHash, 'hex'))
    .update(data)
    .digest('hex');
}

/** 稳定的 32 位键哈希（FNV-1a），用于把 key 映射到固定页 */
export function keyToPageId(key: string, pageCount: number): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < key.length; i++) {
    h ^= key.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return (h >>> 0) % pageCount;
}
