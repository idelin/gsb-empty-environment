/**
 * 确定性规范化编码与哈希。
 *
 * 所有跨域签名均针对规范化后的字节串计算，保证不同实现 / 不同 JSON
 * 键顺序下的验证结果一致。键排序使用 UTF-16 码元字典序，数字使用
 * 最短精确表示，undefined 字段在规范化阶段即被剔除。
 */

import { createHash } from 'node:crypto';

type CanonicalValue =
  | null
  | boolean
  | number
  | string
  | CanonicalValue[]
  | { [key: string]: CanonicalValue };

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function normalizeNumber(value: number): string {
  if (!Number.isFinite(value)) {
    throw new TypeError('canonical encoding rejects NaN / Infinity');
  }
  if (Number.isInteger(value)) {
    return String(value);
  }
  // 最短往返表示，保证确定性。
  const shortest = JSON.stringify(value);
  if (Number(shortest) !== value) {
    throw new TypeError(`non-roundtrip-safe number: ${value}`);
  }
  return shortest;
}

function normalizeNode(value: unknown): CanonicalValue {
  if (value === null || typeof value === 'boolean' || typeof value === 'string') {
    return value;
  }
  if (typeof value === 'number') {
    return JSON.parse(normalizeNumber(value)) as number;
  }
  if (Array.isArray(value)) {
    return value.map((entry) => normalizeNode(entry));
  }
  if (isPlainObject(value)) {
    const result: Record<string, CanonicalValue> = {};
    for (const key of Object.keys(value).sort()) {
      const entry = value[key];
      if (entry === undefined) {
        continue;
      }
      result[key] = normalizeNode(entry);
    }
    return result;
  }
  throw new TypeError(`canonical encoding rejects value of type ${typeof value}`);
}

/** 返回规范化 JSON 字符串。 */
export function canonicalize(value: unknown): string {
  return JSON.stringify(normalizeNode(value));
}

/** 返回规范化字节串（UTF-8）。 */
export function canonicalBytes(value: unknown): Buffer {
  return Buffer.from(canonicalize(value), 'utf8');
}

/** 对规范化字节串计算 SHA-256，输出 base64url。 */
export function hashCanonical(value: unknown): string {
  return createHash('sha256').update(canonicalBytes(value)).digest('base64url');
}

/** 对已规范化字符串计算 SHA-256，输出 base64url。 */
export function hashText(text: string): string {
  return createHash('sha256').update(Buffer.from(text, 'utf8')).digest('base64url');
}

/** 深拷贝（经规范化往返，同时剔除 undefined）。 */
export function structuredCloneCanonical<T>(value: T): T {
  return JSON.parse(canonicalize(value)) as T;
}
