// Schema parsing and validation.
//
// Anything the checker cannot reason about is reported as `unsupported`
// (never silently treated as compatible):
//   - unknown type kinds or unknown keywords (they might carry rules we
//     cannot evaluate),
//   - any reference mechanism ($ref / definitions / types) which could
//     introduce recursion,
//   - missing explicit bounds (numeric ranges, string/array length caps,
//     required flags) which this system requires to be stated explicitly.
// Structurally broken schemas (duplicate field IDs, min > max, defaults
// that do not even parse as their type) are reported as `invalid`.
// A default that parses but violates its own constraints is a *warning*:
// it is handled semantically (absence of the field is then not accepted).

import { INT64_MAX, INT64_MIN, UINT64_MAX } from './types.ts';
import type { Field, Schema, TypeNode } from './types.ts';
import { jsonToValue } from './value.ts';

export type IssueLevel = 'unsupported' | 'invalid' | 'warning';

export interface SchemaIssue {
  level: IssueLevel;
  path: string;
  message: string;
}

export interface ParsedSchema {
  schema: Schema | null;
  issues: SchemaIssue[];
}

const MAX_BOUND = 0x7fffffff; // sanity cap for length bounds

function isPlainObject(x: unknown): x is Record<string, unknown> {
  return typeof x === 'object' && x !== null && !Array.isArray(x);
}

function parseBigIntLiteral(x: unknown): bigint | null {
  if (typeof x === 'bigint') return x;
  if (typeof x === 'number') {
    if (!Number.isInteger(x)) return null;
    return BigInt(x);
  }
  if (typeof x === 'string' && /^-?\d+$/.test(x)) return BigInt(x);
  return null;
}

function parseLengthBound(x: unknown): number | null {
  if (typeof x !== 'number' || !Number.isInteger(x)) return null;
  if (x < 0 || x > MAX_BOUND) return null;
  return x;
}

const FORBIDDEN_KEYS = ['$ref', 'definitions', '$defs', 'types'];

export function parseSchema(raw: unknown): ParsedSchema {
  const issues: SchemaIssue[] = [];
  let schema: Schema | null = null;

  if (!isPlainObject(raw)) {
    issues.push({ level: 'invalid', path: '$', message: 'schema must be an object' });
    return { schema, issues };
  }
  const allowedTop = new Set(['version', 'root']);
  for (const k of Object.keys(raw)) {
    if (FORBIDDEN_KEYS.includes(k)) {
      issues.push({ level: 'unsupported', path: '$', message: `reference mechanism "${k}" is not supported (recursion is forbidden)` });
    } else if (!allowedTop.has(k)) {
      issues.push({ level: 'unsupported', path: '$', message: `unknown top-level keyword "${k}"` });
    }
  }
  if (typeof raw.version !== 'number' || !Number.isInteger(raw.version) || raw.version < 0) {
    issues.push({ level: 'invalid', path: '$.version', message: 'schema version must be a non-negative integer' });
  }
  if (!('root' in raw)) {
    issues.push({ level: 'invalid', path: '$.root', message: 'schema must define "root"' });
  } else {
    const root = parseType(raw.root, '$.root', issues);
    if (root && typeof raw.version === 'number') {
      schema = { version: raw.version as number, root };
    }
  }
  if (issues.some((i) => i.level !== 'warning')) schema = null;
  return { schema, issues };
}

function checkExtraKeys(
  node: Record<string, unknown>,
  allowed: string[],
  path: string,
  issues: SchemaIssue[],
): void {
  for (const k of Object.keys(node)) {
    if (FORBIDDEN_KEYS.includes(k)) {
      issues.push({ level: 'unsupported', path, message: `reference mechanism "${k}" is not supported (recursion is forbidden)` });
    } else if (!allowed.includes(k)) {
      issues.push({ level: 'unsupported', path, message: `unknown keyword "${k}" on type node` });
    }
  }
}

function parseType(raw: unknown, path: string, issues: SchemaIssue[]): TypeNode | null {
  if (!isPlainObject(raw)) {
    issues.push({ level: 'invalid', path, message: 'type node must be an object' });
    return null;
  }
  const kind = raw.kind;
  if (typeof kind !== 'string') {
    issues.push({ level: 'invalid', path: `${path}.kind`, message: 'type node must have a string "kind"' });
    return null;
  }
  switch (kind) {
    case 'bool':
      checkExtraKeys(raw, ['kind'], path, issues);
      return { kind: 'bool' };

    case 'int64':
    case 'uint64': {
      checkExtraKeys(raw, ['kind', 'min', 'max'], path, issues);
      const lo = parseBigIntLiteral(raw.min);
      const hi = parseBigIntLiteral(raw.max);
      if (lo === null || hi === null) {
        issues.push({
          level: 'unsupported',
          path,
          message: `${kind} requires explicit integer "min" and "max" bounds`,
        });
        return null;
      }
      const glo = kind === 'int64' ? INT64_MIN : 0n;
      const ghi = kind === 'int64' ? INT64_MAX : UINT64_MAX;
      if (lo < glo || hi > ghi) {
        issues.push({ level: 'invalid', path, message: `bounds [${lo}, ${hi}] outside ${kind} domain` });
        return null;
      }
      if (lo > hi) {
        issues.push({ level: 'invalid', path, message: `min (${lo}) > max (${hi})` });
        return null;
      }
      return { kind, min: lo, max: hi };
    }

    case 'string': {
      checkExtraKeys(raw, ['kind', 'maxLength'], path, issues);
      const m = parseLengthBound(raw.maxLength);
      if (m === null) {
        issues.push({
          level: 'unsupported',
          path,
          message: 'string requires an explicit "maxLength" (Unicode code points)',
        });
        return null;
      }
      return { kind: 'string', maxLength: m };
    }

    case 'bytes': {
      checkExtraKeys(raw, ['kind', 'maxLength'], path, issues);
      const m = parseLengthBound(raw.maxLength);
      if (m === null) {
        issues.push({
          level: 'unsupported',
          path,
          message: 'bytes requires an explicit "maxLength" (bytes)',
        });
        return null;
      }
      return { kind: 'bytes', maxLength: m };
    }

    case 'array': {
      checkExtraKeys(raw, ['kind', 'items', 'minLength', 'maxLength'], path, issues);
      const minLen = parseLengthBound(raw.minLength);
      const maxLen = parseLengthBound(raw.maxLength);
      if (minLen === null || maxLen === null) {
        issues.push({
          level: 'unsupported',
          path,
          message: 'array requires explicit "minLength" and "maxLength"',
        });
        return null;
      }
      if (minLen > maxLen) {
        issues.push({ level: 'invalid', path, message: `minLength (${minLen}) > maxLength (${maxLen})` });
        return null;
      }
      if (!('items' in raw)) {
        issues.push({ level: 'invalid', path: `${path}.items`, message: 'array requires "items"' });
        return null;
      }
      const items = parseType(raw.items, `${path}.items`, issues);
      if (!items) return null;
      return { kind: 'array', items, minLength: minLen, maxLength: maxLen };
    }

    case 'record': {
      checkExtraKeys(raw, ['kind', 'fields'], path, issues);
      if (!Array.isArray(raw.fields)) {
        issues.push({ level: 'invalid', path: `${path}.fields`, message: 'record requires a "fields" array' });
        return null;
      }
      const seen = new Set<number>();
      const fields: Field[] = [];
      let ok = true;
      raw.fields.forEach((fraw, i) => {
        const fpath = `${path}.fields[${i}]`;
        if (!isPlainObject(fraw)) {
          issues.push({ level: 'invalid', path: fpath, message: 'field must be an object' });
          ok = false;
          return;
        }
        checkExtraKeys(fraw, ['id', 'name', 'required', 'type', 'default'], fpath, issues);
        const id = fraw.id;
        if (typeof id !== 'number' || !Number.isInteger(id) || id < 0 || id > MAX_BOUND) {
          issues.push({ level: 'invalid', path: `${fpath}.id`, message: 'field id must be a non-negative integer' });
          ok = false;
          return;
        }
        if (seen.has(id)) {
          issues.push({ level: 'invalid', path: `${fpath}.id`, message: `duplicate field id ${id}` });
          ok = false;
          return;
        }
        seen.add(id);
        if (fraw.name !== undefined && typeof fraw.name !== 'string') {
          issues.push({ level: 'invalid', path: `${fpath}.name`, message: 'field name must be a string (display only)' });
          ok = false;
          return;
        }
        if (typeof fraw.required !== 'boolean') {
          issues.push({
            level: 'unsupported',
            path: `${fpath}.required`,
            message: 'field requires an explicit boolean "required"',
          });
          ok = false;
          return;
        }
        if (!('type' in fraw)) {
          issues.push({ level: 'invalid', path: `${fpath}.type`, message: 'field requires "type"' });
          ok = false;
          return;
        }
        const type = parseType(fraw.type, `${fpath}.type`, issues);
        if (!type) {
          ok = false;
          return;
        }
        const hasDefault = Object.prototype.hasOwnProperty.call(fraw, 'default');
        const field: Field = {
          id,
          name: fraw.name as string | undefined,
          required: fraw.required as boolean,
          hasDefault,
          defaultValue: fraw.default,
          type,
        };
        if (hasDefault) {
          try {
            jsonToValue(fraw.default, type); // parse check only; constraint check is semantic
          } catch (e) {
            issues.push({
              level: 'invalid',
              path: `${fpath}.default`,
              message: `default does not parse as its type: ${(e as Error).message}`,
            });
            ok = false;
            return;
          }
        }
        fields.push(field);
      });
      if (!ok) return null;
      return { kind: 'record', fields };
    }

    default:
      issues.push({ level: 'unsupported', path, message: `unknown type kind "${kind}"` });
      return null;
  }
}
