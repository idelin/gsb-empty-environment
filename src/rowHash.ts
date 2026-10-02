import { createHash } from 'node:crypto';

/** 数据行：必须包含数值主键 id，其余字段任意。 */
export type Row = { id: number } & Record<string, unknown>;

/**
 * 行的规范化序列化：字段名排序后拼接，保证同一逻辑行在任何时刻
 * 得到完全相同的字节序列（与字段插入顺序无关）。
 */
export function canonicalize(row: Row): string {
  const keys = Object.keys(row).sort();
  const parts: string[] = [];
  for (const key of keys) {
    parts.push(`${key}=${JSON.stringify(row[key]) ?? 'undefined'}`);
  }
  return parts.join('|');
}

/**
 * 计算单行校验和：SHA-256 截断为 128 bit（32 个十六进制字符）。
 * 128 bit 下数十万行异或聚合的碰撞概率可忽略（生日界 ~2^64）。
 */
export function rowChecksum(row: Row): string {
  return createHash('sha256').update(canonicalize(row), 'utf8').digest('hex').slice(0, 32);
}

export const ZERO_CHECKSUM = '0'.repeat(32);

/** 两个等长十六进制校验和按字节异或。 */
export function xorChecksum(a: string, b: string): string {
  const ba = Buffer.from(a, 'hex');
  const bb = Buffer.from(b, 'hex');
  const out = Buffer.alloc(ba.length);
  for (let i = 0; i < ba.length; i++) out[i] = ba[i] ^ bb[i];
  return out.toString('hex');
}
