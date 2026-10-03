/** 确定性 JSON 序列化（键排序），用于哈希承诺。 */
export function canonical(value: unknown): string {
  if (value === null || typeof value !== 'object') {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return '[' + value.map((item) => canonical(item)).join(',') + ']';
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  const parts = keys.map((key) => JSON.stringify(key) + ':' + canonical(record[key]));
  return '{' + parts.join(',') + '}';
}
