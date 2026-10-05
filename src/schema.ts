import { ConfigError } from './errors.ts';
import type { SourceRef } from './origin.ts';
import { SecretValue } from './secret.ts';

/**
 * Type tags:
 *   'string' | 'number' | 'boolean' | 'string[]' | 'number[]' | 'boolean[]'
 *   'secret:<type>'  wraps the value in SecretValue
 *   '<type>?'        optional (may be absent)
 *   '<type>|null'    nullable (may be explicitly null)
 * Nested objects are described with plain nested objects.
 */
export type SchemaTypeTag = string;
export type SchemaNode = SchemaTypeTag | { [key: string]: SchemaNode };
export type Schema = Record<string, SchemaNode>;

export interface ParsedTag {
  base: 'string' | 'number' | 'boolean';
  isArray: boolean;
  isSecret: boolean;
  optional: boolean;
  nullable: boolean;
}

export function parseTag(raw: string, path: string): ParsedTag {
  let tag = raw.trim();
  let isSecret = false;
  if (tag.startsWith('secret:')) {
    isSecret = true;
    tag = tag.slice('secret:'.length);
  }
  let optional = false;
  if (tag.endsWith('?')) {
    optional = true;
    tag = tag.slice(0, -1);
  }
  let nullable = false;
  if (tag.endsWith('|null')) {
    nullable = true;
    tag = tag.slice(0, -'|null'.length);
  }
  let isArray = false;
  if (tag.endsWith('[]')) {
    isArray = true;
    tag = tag.slice(0, -2);
  }
  if (tag !== 'string' && tag !== 'number' && tag !== 'boolean') {
    throw new ConfigError('TYPE_MISMATCH', `Unknown schema type ${JSON.stringify(raw)}`, { path });
  }
  return { base: tag, isArray, isSecret, optional, nullable };
}

export function isLeafTag(node: SchemaNode): node is SchemaTypeTag {
  return typeof node === 'string';
}

/** Convert a raw value to the schema type. Throws ConfigError with source on failure. */
export function convertValue(raw: unknown, tag: ParsedTag, path: string, source?: SourceRef): unknown {
  if (raw === null) {
    if (tag.nullable) return null;
    throw new ConfigError('NULL_VALUE', `Null is not allowed for type ${describeTag(tag)}`, { path, source });
  }
  if (raw === undefined) {
    if (tag.optional) return undefined;
    throw new ConfigError('NULL_VALUE', `Missing required value of type ${describeTag(tag)}`, { path, source });
  }
  const convertOne = (v: unknown, p: string): unknown => convertScalar(v, tag, p, source);
  let value: unknown;
  if (tag.isArray) {
    const items = Array.isArray(raw) ? raw : [raw];
    value = items.map((v, i) => convertOne(v, `${path}[${i}]`));
  } else {
    if (Array.isArray(raw)) {
      throw new ConfigError('TYPE_MISMATCH', `Expected ${describeTag(tag)} but got an array`, { path, source });
    }
    value = convertOne(raw, path);
  }
  return tag.isSecret ? new SecretValue(value) : value;
}

function convertScalar(raw: unknown, tag: ParsedTag, path: string, source?: SourceRef): unknown {
  switch (tag.base) {
    case 'string': {
      if (typeof raw === 'string') return raw;
      if (typeof raw === 'number' || typeof raw === 'boolean') return String(raw);
      throw conversionError(raw, tag, path, source);
    }
    case 'number': {
      if (typeof raw === 'number') return raw;
      if (typeof raw === 'string' && raw.trim() !== '') {
        const num = Number(raw);
        if (!Number.isNaN(num)) return num;
      }
      throw conversionError(raw, tag, path, source);
    }
    case 'boolean': {
      if (typeof raw === 'boolean') return raw;
      if (typeof raw === 'string') {
        const lowered = raw.trim().toLowerCase();
        if (['true', '1', 'yes', 'on'].includes(lowered)) return true;
        if (['false', '0', 'no', 'off'].includes(lowered)) return false;
      }
      throw conversionError(raw, tag, path, source);
    }
  }
}

function conversionError(raw: unknown, tag: ParsedTag, path: string, source?: SourceRef): ConfigError {
  return new ConfigError(
    'TYPE_CONVERSION',
    `Cannot convert ${JSON.stringify(raw)} to ${describeTag(tag)}`,
    { path, source, details: { raw, expected: describeTag(tag) } },
  );
}

function describeTag(tag: ParsedTag): string {
  let out = tag.base;
  if (tag.isArray) out += '[]';
  if (tag.isSecret) out = `secret:${out}`;
  return out;
}
