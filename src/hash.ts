/**
 * 与对象键顺序无关的稳定序列化 + FNV-1a 32 位指纹。
 * 浏览器环境不保证保留对象插入顺序之外的稳定性，这里对普通对象的键
 * 显式排序，保证同一逻辑内容（不同键序）得到相同指纹。
 */

function stableStringify(value: unknown): string {
  if (value === null) return "null";
  if (value === undefined) return "undefined";
  const type = typeof value;
  if (type === "number" || type === "boolean" || type === "bigint") {
    return `${type}:${String(value)}`;
  }
  if (type === "string") return `string:${value as string}`;
  if (type === "function" || type === "symbol") {
    // 业务内容不应包含函数/symbol；给一个稳定占位，避免抛异常。
    return `${type}:`;
  }
  if (Array.isArray(value)) {
    return `array:[${value.map((item) => stableStringify(item)).join(",")}]`;
  }
  if (value instanceof Date) return `date:${value.toISOString()}`;
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  const body = keys
    .map((key) => `${JSON.stringify(key)}:${stableStringify(record[key])}`)
    .join(",");
  return `object:{${body}}`;
}

/** FNV-1a 32 位，输出 8 位十六进制字符串。 */
export function stableHash(value: unknown): string {
  const serialized = stableStringify(value);
  let hash = 0x811c9dc5;
  for (let index = 0; index < serialized.length; index += 1) {
    hash ^= serialized.charCodeAt(index);
    // 32 位无符号乘法：Math.imul 保证按模 2^32 运算。
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(16).padStart(8, "0");
}
