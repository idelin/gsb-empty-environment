/**
 * 语义值比较与稳定序列化。
 *
 * 规则：
 * - bigint 按数值比较；number 严格相等（NaN 视为相等）。
 * - Uint8Array 按字节序列比较。
 * - 普通对象按 key 排序后比较，与字段书写顺序无关。
 * - 数组按序比较（顺序差异由树层的 ordered 规则负责，值层不做无序化）。
 * - Map/Set 以稳定字符串形式比较。
 */

function isByteArray(value: unknown): value is Uint8Array {
  return value instanceof Uint8Array;
}

function plainObjectKeys(value: Record<string, unknown>): string[] {
  return Object.keys(value).sort();
}

/** 稳定 JSON 风格序列化，用于生成无序集合的语义指纹。 */
export function stableStringify(value: unknown): string {
  if (value === null || value === undefined) return JSON.stringify(value ?? null);
  if (typeof value === "bigint") return `"bigint:${value.toString()}"`;
  if (typeof value === "number") {
    return Number.isNaN(value) ? '"nan"' : JSON.stringify(value);
  }
  if (typeof value === "string" || typeof value === "boolean") {
    return JSON.stringify(value);
  }
  if (isByteArray(value)) {
    let hex = "";
    for (const byte of value) hex += byte.toString(16).padStart(2, "0");
    return `"bytes:${hex}"`;
  }
  if (value instanceof Map) {
    const entries = [...value.entries()].map(
      ([key, val]) => `[${stableStringify(key)},${stableStringify(val)}]`,
    );
    entries.sort();
    return `{map:[${entries.join(",")}]}`;
  }
  if (value instanceof Set) {
    const entries = [...value].map(stableStringify);
    entries.sort();
    return `{set:[${entries.join(",")}]}`;
  }
  if (Array.isArray(value)) {
    return `[${value.map(stableStringify).join(",")}]`;
  }
  if (typeof value === "object") {
    const record = value as Record<string, unknown>;
    const parts = plainObjectKeys(record).map(
      (key) => `${JSON.stringify(key)}:${stableStringify(record[key])}`,
    );
    return `{${parts.join(",")}}`;
  }
  throw new TypeError(`stableStringify: unsupported value type ${typeof value}`);
}

function byteArraysEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i += 1) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

/** 语义等值判断：忽略对象键顺序，保留数组顺序。 */
export function semanticEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a === "bigint" || typeof b === "bigint") {
    return typeof a === typeof b && (a as bigint) === (b as bigint);
  }
  if (typeof a !== typeof b) return false;
  if (a === null || b === null) return a === b;
  if (typeof a === "number") {
    return a === b || (Number.isNaN(a) && Number.isNaN(b));
  }
  if (typeof a !== "object") return a === b;

  if (isByteArray(a) || isByteArray(b)) {
    return isByteArray(a) && isByteArray(b) && byteArraysEqual(a, b);
  }
  if (a instanceof Map || b instanceof Map) {
    return (
      a instanceof Map &&
      b instanceof Map &&
      stableStringify(a) === stableStringify(b)
    );
  }
  if (a instanceof Set || b instanceof Set) {
    return (
      a instanceof Set &&
      b instanceof Set &&
      stableStringify(a) === stableStringify(b)
    );
  }

  const aIsArray = Array.isArray(a);
  const bIsArray = Array.isArray(b);
  if (aIsArray !== bIsArray) return false;
  if (aIsArray && bIsArray) {
    if (a.length !== b.length) return false;
    return a.every((item, index) => semanticEqual(item, b[index]));
  }

  const aRecord = a as Record<string, unknown>;
  const bRecord = b as Record<string, unknown>;
  const aKeys = plainObjectKeys(aRecord);
  const bKeys = plainObjectKeys(bRecord);
  if (aKeys.length !== bKeys.length) return false;
  return aKeys.every((key, index) => {
    if (key !== bKeys[index]) return false;
    return semanticEqual(aRecord[key], bRecord[key]);
  });
}
