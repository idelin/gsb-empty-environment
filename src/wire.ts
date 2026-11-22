import type { FieldSchema, MessageSchema } from './registry.ts';
import type { FieldValue } from './types.ts';

/** 简单 TLV 线格式：field := index(varint) length(varint) bytes。 */

export function encodeVarint(value: number): number[] {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new Error('varint must be a non-negative safe integer');
  }
  const bytes: number[] = [];
  let n = value;
  do {
    let b = n % 128;
    n = Math.floor(n / 128);
    if (n > 0) b += 128;
    bytes.push(b);
  } while (n > 0);
  return bytes;
}

export function readVarint(raw: Uint8Array, pos: number): [number, number] {
  let result = 0;
  let shift = 0;
  let p = pos;
  for (;;) {
    if (p >= raw.length) throw new Error('truncated varint');
    const b = raw[p];
    result += (b % 128) * 2 ** shift;
    p += 1;
    if (b < 128) return [result, p];
    shift += 7;
    if (shift > 49) throw new Error('varint too long');
  }
}

export function encodeMessage(parts: Array<readonly [number, Uint8Array]>): Uint8Array {
  const chunks: number[] = [];
  for (const [index, bytes] of parts) {
    chunks.push(...encodeVarint(index), ...encodeVarint(bytes.length), ...bytes);
  }
  return Uint8Array.from(chunks);
}

export function decodeFields(raw: Uint8Array): Array<[number, Uint8Array]> {
  const out: Array<[number, Uint8Array]> = [];
  let pos = 0;
  while (pos < raw.length) {
    const [index, p1] = readVarint(raw, pos);
    const [length, p2] = readVarint(raw, p1);
    if (p2 + length > raw.length) throw new Error('truncated field');
    out.push([index, raw.slice(p2, p2 + length)]);
    pos = p2 + length;
  }
  return out;
}

const utf8 = new TextDecoder('utf-8', { fatal: true });

function decodeValue(field: FieldSchema, bytes: Uint8Array): FieldValue {
  switch (field.type) {
    case 'string':
      return utf8.decode(bytes);
    case 'varint': {
      const [value, end] = readVarint(bytes, 0);
      if (end !== bytes.length) throw new Error('varint field has trailing bytes');
      return value;
    }
    case 'bool':
      if (bytes.length !== 1 || bytes[0] > 1) throw new Error('invalid bool field');
      return bytes[0] === 1;
    case 'bytes':
      return bytes.slice();
  }
}

/**
 * 按 schema 解码原始字节，返回按 schema 声明顺序排列的命名字段。
 * 未知字段索引被跳过；畸形字节抛出异常（由调用方隔离）。
 */
export function decodeMessage(
  schema: MessageSchema,
  raw: Uint8Array,
): Array<[string, FieldValue]> {
  const byIndex = new Map<number, FieldSchema>();
  for (const field of schema.fields) byIndex.set(field.index, field);
  const values = new Map<string, FieldValue>();
  for (const [index, bytes] of decodeFields(raw)) {
    const field = byIndex.get(index);
    if (field === undefined) continue;
    values.set(field.name, decodeValue(field, bytes));
  }
  const out: Array<[string, FieldValue]> = [];
  for (const field of schema.fields) {
    const value = values.get(field.name);
    if (value !== undefined) out.push([field.name, value]);
  }
  return out;
}
