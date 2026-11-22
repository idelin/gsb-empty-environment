import type { FieldValue } from './types.ts';

export type JsonValue =
  | string
  | number
  | boolean
  | null
  | JsonValue[]
  | { [key: string]: JsonValue };

/** 把字段值转换为规范 JSON 结构（对象键排序、字节转 base64）。 */
export function toCanonical(value: FieldValue): JsonValue {
  if (value === null) return null;
  if (typeof value === 'string' || typeof value === 'boolean') return value;
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new Error('non-finite number is not canonical');
    return value;
  }
  if (value instanceof Uint8Array) {
    return { $bytes: Buffer.from(value).toString('base64') };
  }
  if (Array.isArray(value)) return value.map(toCanonical);
  const out: Record<string, JsonValue> = {};
  for (const key of Object.keys(value).sort()) out[key] = toCanonical(value[key]);
  return out;
}

export function canonicalJson(value: FieldValue): string {
  return JSON.stringify(toCanonical(value));
}
