/**
 * FNV-1a 32 位滚动哈希，用于分片与整事务的完整性校验。
 */
export interface Hasher {
  update(text: string): void;
  digest(): string;
  reset(): void;
}

const FNV_OFFSET = 0x811c9dc5;
const FNV_PRIME = 0x01000193;

export function createHasher(): Hasher {
  let hash = FNV_OFFSET;
  return {
    update(text: string): void {
      for (let i = 0; i < text.length; i++) {
        hash ^= text.charCodeAt(i);
        hash = Math.imul(hash, FNV_PRIME);
      }
    },
    digest(): string {
      return (hash >>> 0).toString(16).padStart(8, '0');
    },
    reset(): void {
      hash = FNV_OFFSET;
    },
  };
}
