import { createHash } from 'node:crypto';

/** 确定性序列化：对象键排序，保证同一内容得到同一字符串。 */
export function stableStringify(value: unknown): string {
  if (value === null || typeof value !== 'object') {
    return JSON.stringify(value) ?? 'undefined';
  }
  if (Array.isArray(value)) {
    return '[' + value.map((item) => stableStringify(item)).join(',') + ']';
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  return (
    '{' +
    keys.map((key) => JSON.stringify(key) + ':' + stableStringify(record[key])).join(',') +
    '}'
  );
}

/** 内容的 SHA-256 摘要，用作缓存键的组成部分。 */
export function digest(value: unknown): string {
  return createHash('sha256').update(stableStringify(value)).digest('hex');
}
