import type { CompiledGraph } from './schema.ts';
import { DecodeError } from './types.ts';

/**
 * 测试/网关出站侧的二进制消息编码器，严格遵循 binary.ts 的线格式。
 * 输入是“字段编号 -> JS 值”的普通对象树（不依赖属性名字符串）。
 */

type Jsonish = string | number | boolean | bigint | Jsonish[] | { [fieldNo: number]: Jsonish };

const textEncoder = new TextEncoder();

export function encodeRootMessage(graph: CompiledGraph, root: Record<number, Jsonish>): Uint8Array {
  return encodeObjectMessage(graph, graph.rootTypeNo, root);
}

function encodeObjectMessage(
  graph: CompiledGraph,
  typeNo: number,
  record: Record<number, Jsonish>,
): Uint8Array {
  const type = graph.types.get(typeNo);
  if (type === undefined || type.kind !== 'object') {
    throw new DecodeError('SCHEMA_INVALID', `type ${typeNo} is not an object`);
  }
  const parts: Uint8Array[] = [];
  putU32(parts, typeNo);
  const fieldNos = Object.keys(record)
    .map((key) => Number(key))
    .sort((a, b) => a - b);
  putU32(parts, fieldNos.length);
  for (const fieldNo of fieldNos) {
    const field = type.fieldsByNo.get(fieldNo);
    if (field === undefined) {
      throw new DecodeError('SCHEMA_INVALID', `field ${fieldNo} not in type ${typeNo}`);
    }
    putU32(parts, fieldNo);
    const valueBytes = encodeValue(graph, field.typeNo, record[fieldNo]);
    putU32(parts, valueBytes.length);
    parts.push(valueBytes);
  }
  return concat(parts);
}

function encodeValue(graph: CompiledGraph, typeNo: number, value: Jsonish): Uint8Array {
  const type = graph.types.get(typeNo);
  if (type === undefined) {
    throw new DecodeError('SCHEMA_INVALID', `value type ${typeNo} missing`);
  }
  const parts: Uint8Array[] = [];
  switch (type.kind) {
    case 'bool':
      if (typeof value !== 'boolean') {
        throw new DecodeError('TYPE_MISMATCH', `bool type ${typeNo} got ${typeof value}`);
      }
      return Uint8Array.of(value ? 1 : 0);
    case 'int32': {
      if (typeof value !== 'number' || !Number.isInteger(value)) {
        throw new DecodeError('TYPE_MISMATCH', `int32 type ${typeNo} got non-integer`);
      }
      const buffer = new ArrayBuffer(4);
      new DataView(buffer).setInt32(0, value, false);
      return new Uint8Array(buffer);
    }
    case 'id64': {
      let big: bigint;
      if (typeof value === 'bigint') {
        big = value;
      } else if (typeof value === 'number' && Number.isSafeInteger(value)) {
        big = BigInt(value);
      } else if (typeof value === 'string') {
        big = BigInt(value);
      } else {
        throw new DecodeError('TYPE_MISMATCH', `id64 type ${typeNo} cannot encode ${typeof value}`);
      }
      const buffer = new ArrayBuffer(8);
      new DataView(buffer).setBigUint64(0, bigAsUnsigned(big), false);
      return new Uint8Array(buffer);
    }
    case 'string':
      if (typeof value !== 'string') {
        throw new DecodeError('TYPE_MISMATCH', `string type ${typeNo} got ${typeof value}`);
      }
      return textEncoder.encode(value);
    case 'object': {
      if (typeof value !== 'object' || value === null || Array.isArray(value)) {
        throw new DecodeError('TYPE_MISMATCH', `object type ${typeNo} got non-object`);
      }
      return encodeObjectMessage(graph, typeNo, value as Record<number, Jsonish>);
    }
    case 'array': {
      if (!Array.isArray(value)) {
        throw new DecodeError('TYPE_MISMATCH', `array type ${typeNo} got non-array`);
      }
      putU32(parts, value.length);
      for (const element of value) {
        const encoded = encodeValue(graph, type.elementTypeNo, element);
        putU32(parts, encoded.length);
        parts.push(encoded);
      }
      return concat(parts);
    }
  }
}

function bigAsUnsigned(value: bigint): bigint {
  if (value >= 0n) {
    return value;
  }
  return value + (1n << 64n);
}

function putU32(parts: Uint8Array[], value: number): void {
  const buffer = new ArrayBuffer(4);
  new DataView(buffer).setUint32(0, value, false);
  parts.push(new Uint8Array(buffer));
}

function concat(parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((sum, part) => sum + part.length, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    out.set(part, offset);
    offset += part.length;
  }
  return out;
}
