import { createHash } from 'node:crypto';

/**
 * 规范化序列化：对象键按字典序排序，递归处理数组/嵌套对象。
 * 同一内存状态必然产生逐字节一致的字符串，是可对比快照的基础。
 */
export function canonicalize(value: unknown): string {
  return JSON.stringify(sortValue(value));
}

function sortValue(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(sortValue);
  }
  if (value !== null && typeof value === 'object') {
    const sorted: Record<string, unknown> = {};
    for (const key of Object.keys(value as Record<string, unknown>).sort()) {
      sorted[key] = sortValue((value as Record<string, unknown>)[key]);
    }
    return sorted;
  }
  return value;
}

/** 状态的 SHA-256 指纹 */
export function hashState(value: unknown): string {
  return createHash('sha256').update(canonicalize(value), 'utf8').digest('hex');
}
