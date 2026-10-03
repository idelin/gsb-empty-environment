import { createHash } from 'node:crypto';

export function stableStringify(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value) ?? 'undefined';
  if (Array.isArray(value)) return '[' + value.map(stableStringify).join(',') + ']';
  const obj = value as Record<string, unknown>;
  return (
    '{' +
    Object.keys(obj)
      .sort()
      .map((k) => JSON.stringify(k) + ':' + stableStringify(obj[k]))
      .join(',') +
    '}'
  );
}

export function sha256(text: string): string {
  return createHash('sha256').update(text).digest('hex');
}

// 64-bit FNV-1a rolling hash; cheap enough to run over 1M+ log records.
export class Fnv1a64 {
  private hash = 0xcbf29ce484222325n;
  update(text: string): void {
    for (let i = 0; i < text.length; i++) {
      this.hash ^= BigInt(text.charCodeAt(i));
      this.hash = (this.hash * 0x100000001b3n) & 0xffffffffffffffffn;
    }
  }
  digest(): string {
    return this.hash.toString(16).padStart(16, '0');
  }
}
