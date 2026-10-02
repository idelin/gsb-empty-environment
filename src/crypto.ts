import { createHash, randomBytes } from 'node:crypto';

/** 带域分离的 SHA-256，避免叶/内部节点/擦除标记之间发生第二原像混淆。 */
export function hashDomain(domain: string, ...parts: Buffer[]): Buffer {
  const h = createHash('sha256');
  h.update(domain, 'utf8');
  for (const p of parts) h.update(p);
  return h.digest();
}

export const hashLeaf = (payload: Buffer): Buffer => hashDomain('leaf/v1', payload);
export const hashNode = (left: Buffer, right: Buffer): Buffer => hashDomain('node/v1', left, right);
export const hashEraseMarker = (leafIndex: number, originalLeafHash: Buffer, signature: Buffer): Buffer =>
  hashDomain('erase/v1', u64(leafIndex), originalLeafHash, signature);

export function u64(n: number): Buffer {
  const b = Buffer.alloc(8);
  b.writeBigUInt64BE(BigInt(n));
  return b;
}

/** 确定性 JSON 序列化（键排序），保证同一记录永远得到同一叶子哈希。 */
export function canonicalJson(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value);
  if (Array.isArray(value)) return '[' + value.map(canonicalJson).join(',') + ']';
  const obj = value as Record<string, unknown>;
  const keys = Object.keys(obj).sort();
  return '{' + keys.map((k) => JSON.stringify(k) + ':' + canonicalJson(obj[k])).join(',') + '}';
}

export const toHex = (b: Buffer): string => b.toString('hex');
export const fromHex = (s: string): Buffer => Buffer.from(s, 'hex');
export const randomId = (): string => randomBytes(8).toString('hex');
