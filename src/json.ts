/**
 * JSON 兼容树工具。
 * - 对象键无序：规范化时按键名排序后哈希。
 * - 数组有序：保持原顺序。
 */
import { createHash } from 'node:crypto';

export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonObject = { [key: string]: JsonValue };

export function isObject(v: JsonValue): v is JsonObject {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

export function deepClone<T extends JsonValue>(v: T): T {
  return JSON.parse(JSON.stringify(v)) as T;
}

/** 规范化 JSON：对象键排序（键无序），数组保持顺序。 */
export function canonicalize(value: JsonValue): string {
  if (value === null) return 'null';
  if (typeof value === 'number') {
    if (Number.isFinite(value)) return JSON.stringify(value);
    throw new TypeError('non-finite number is not JSON-compatible');
  }
  if (typeof value === 'boolean' || typeof value === 'string') {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return '[' + value.map(canonicalize).join(',') + ']';
  }
  const obj = value as JsonObject;
  const keys = Object.keys(obj).sort();
  const parts = keys.map((k) => JSON.stringify(k) + ':' + canonicalize(obj[k] as JsonValue));
  return '{' + parts.join(',') + '}';
}

/** SHA-256（hex）作为内容哈希；键顺序不同、数组顺序不同都会区分。 */
export function canonicalHash(value: JsonValue): string {
  return createHash('sha256').update(canonicalize(value)).digest('hex');
}

export function deepEqual(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((item, i) => deepEqual(item, b[i] as JsonValue));
  }
  if (isObject(a) && isObject(b)) {
    const ak = Object.keys(a);
    const bk = Object.keys(b);
    if (ak.length !== bk.length) return false;
    return ak.every((k) => k in b && deepEqual(a[k] as JsonValue, b[k] as JsonValue));
  }
  return false;
}

/** RFC 6901 JSON Pointer：转义键中的 ~ 与 /。 */
export function escapeToken(token: string): string {
  return token.replace(/~/g, '~0').replace(/\//g, '~1');
}

export function unescapeToken(token: string): string {
  return token.replace(/~1/g, '/').replace(/~0/g, '~');
}

/** 形如 "/a/b/0" -> ["a","b","0"]；"" 表示根。 */
export function parsePointer(pointer: string): string[] {
  if (pointer === '') return [];
  if (!pointer.startsWith('/')) {
    throw new TypeError(`invalid JSON Pointer: ${pointer}`);
  }
  return pointer.slice(1).split('/').map(unescapeToken);
}

/** 沿 pointer 取值；返回 { found, value }，缺失（含越界）时 found=false。 */
export function getPointer(value: JsonValue, pointer: string): { found: boolean; value?: JsonValue } {
  let cur: JsonValue = value;
  const tokens = parsePointer(pointer);
  for (const token of tokens) {
    if (Array.isArray(cur)) {
      const idx = Number(token);
      if (!/^(0|[1-9][0-9]*)$/.test(token) || idx >= cur.length) return { found: false };
      cur = cur[idx] as JsonValue;
    } else if (isObject(cur)) {
      if (!(token in cur)) return { found: false };
      cur = cur[token] as JsonValue;
    } else {
      return { found: false };
    }
  }
  return { found: true, value: cur };
}
