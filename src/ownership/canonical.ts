import { createHash } from 'node:crypto';
import type { OwnershipEvent } from './types.ts';

/**
 * 确定性规范化 JSON：对象键按 UTF-16 码点排序，无多余空白。
 * 签名与事件哈希都基于该字节序列，保证跨投递者/跨进程一致。
 */
export function canonicalJson(value: unknown): string {
  if (value === null || typeof value !== 'object') {
    return JSON.stringify(value ?? null);
  }
  if (Array.isArray(value)) {
    const items = value.map((item) => canonicalJson(item));
    return `[${items.join(',')}]`;
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  const parts = keys.map((key) => `${JSON.stringify(key)}:${canonicalJson(record[key])}`);
  return `{${parts.join(',')}}`;
}

export function sha256Base64Url(input: string | Buffer): string {
  const hash = createHash('sha256');
  hash.update(input);
  return hash.digest('base64url');
}

/** 事件体的规范化字节。 */
export function canonicalEventBytes(body: OwnershipEvent): Buffer {
  return Buffer.from(canonicalJson(body), 'utf8');
}

/** 计算事件哈希：sha256(canonical(body)) 的 base64url。 */
export function computeEventHash(body: OwnershipEvent): string {
  return sha256Base64Url(canonicalEventBytes(body));
}
