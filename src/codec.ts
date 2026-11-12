import type { MessageData } from './types.ts';

/**
 * data_bytes 的默认编解码器。
 *
 * 真实 hub 一般使用 protobuf 等二进制编码；调用方可以在 ValidatorDeps
 * 中注入自定义 encode/decode。这里给出开箱即用的规范 JSON 实现：
 * 对象键递归排序、Uint8Array 以带标签的 base64 形式承载，保证编码确定。
 */

const BYTES_TAG = '__bytes__';

interface EncodedBytes {
  [BYTES_TAG]: string;
}

function isBytes(value: unknown): value is Uint8Array {
  return value instanceof Uint8Array;
}

function toBase64(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary);
}

function fromBase64(encoded: string): Uint8Array {
  const binary = atob(encoded);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}

type JsonValue =
  | string
  | number
  | boolean
  | null
  | JsonValue[]
  | { [key: string]: JsonValue };

function toCanonicalJson(value: unknown): JsonValue {
  if (value === null || value === undefined) {
    return null;
  }
  if (isBytes(value)) {
    const tagged: EncodedBytes = { [BYTES_TAG]: toBase64(value) };
    return tagged;
  }
  if (Array.isArray(value)) {
    return value.map((entry) => toCanonicalJson(entry));
  }
  if (typeof value === 'object') {
    const source = value as Record<string, unknown>;
    const sorted: Record<string, JsonValue> = {};
    for (const key of Object.keys(source).sort()) {
      sorted[key] = toCanonicalJson(source[key]);
    }
    return sorted;
  }
  return value as JsonValue;
}

function fromCanonicalJson(value: unknown): unknown {
  if (value === null || value === undefined) {
    return null;
  }
  if (Array.isArray(value)) {
    return value.map((entry) => fromCanonicalJson(entry));
  }
  if (typeof value === 'object') {
    const record = value as Record<string, unknown>;
    if (typeof record[BYTES_TAG] === 'string' && Object.keys(record).length === 1) {
      return fromBase64(record[BYTES_TAG] as string);
    }
    const restored: Record<string, unknown> = {};
    for (const [key, entry] of Object.entries(record)) {
      restored[key] = fromCanonicalJson(entry);
    }
    return restored;
  }
  return value;
}

export function encodeData(data: MessageData): Uint8Array {
  const canonical = toCanonicalJson(data);
  return new TextEncoder().encode(JSON.stringify(canonical));
}

export function decodeData(bytes: Uint8Array): MessageData {
  const parsed = JSON.parse(new TextDecoder().decode(bytes)) as unknown;
  return fromCanonicalJson(parsed) as MessageData;
}
