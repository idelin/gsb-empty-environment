import { BodyParseError } from './errors.ts';
import { parseRawJson, type RawNode } from './raw-parser.ts';

export { BodyParseError } from './errors.ts';
export type { ParseErrorCode } from './errors.ts';

/**
 * Field kinds supported by the declaration table:
 * - id64:    64-bit signed identifier, surfaced as a canonical decimal string.
 * - safeInt: integer guaranteed to be a JavaScript safe integer.
 * - string:  ordinary JSON string.
 * - number:  ordinary JSON number (floats and exponents allowed).
 */
export type FieldKind = 'id64' | 'safeInt' | 'string' | 'number';

export type FieldDeclarations = Readonly<Record<string, FieldKind>>;

export type ParsedValue =
  | string
  | number
  | boolean
  | null
  | ParsedValue[]
  | { [key: string]: ParsedValue };

const INT64_MIN = -9223372036854775808n;
const INT64_MAX = 9223372036854775807n;

// Plain optional-sign decimal integer, no exponents and no leading zero
// (the single digit "0" is allowed).
const PLAIN_INTEGER_RE = /^-?(?:0|[1-9]\d*)$/;

/**
 * Parse a request body from its raw JSON text.
 *
 * `id64` fields never round-trip through a floating-point number: the result
 * contains a canonical decimal string suitable for exact authorization checks.
 */
export function parseRequestBody(text: string, declarations: FieldDeclarations): ParsedValue {
  if (typeof text !== 'string') {
    throw new BodyParseError('syntax', 'Request body must be a string', '', 0);
  }
  const root = parseRawJson(text);
  return convertNode(root, declarations, '');
}

function convertNode(node: RawNode, declarations: FieldDeclarations, path: string): ParsedValue {
  switch (node.kind) {
    case 'object': {
      const result: Record<string, ParsedValue> = Object.create(null) as Record<string, ParsedValue>;
      for (const entry of node.entries) {
        const childPath = joinPath(path, entry.key);
        const kind = Object.prototype.hasOwnProperty.call(declarations, entry.key)
          ? declarations[entry.key]
          : undefined;
        const value =
          kind === undefined
            ? convertUndeclared(entry.value, childPath)
            : convertDeclared(entry.value, kind, childPath);
        result[entry.key] = value;
      }
      return result;
    }
    case 'array':
      return node.items.map((item, index) =>
        convertUndeclared(item, joinPath(path, String(index))),
      );
    default:
      return convertUndeclared(node, path);
  }
}

function convertDeclared(node: RawNode, kind: FieldKind, path: string): ParsedValue {
  switch (kind) {
    case 'id64':
      return convertId64(node, path);
    case 'safeInt':
      return convertSafeInt(node, path);
    case 'string':
      if (node.kind !== 'string') {
        throw new BodyParseError('type_mismatch', "Expected string for 'string' field", path, node.index);
      }
      return node.value;
    case 'number':
      return convertNumber(node, path);
  }
}

function convertId64(node: RawNode, path: string): string {
  if (node.kind === 'string') {
    if (!PLAIN_INTEGER_RE.test(node.value)) {
      throw new BodyParseError(
        'id64_format',
        "id64 string must be a plain decimal integer without leading zeroes (e.g. '9007199254740993')",
        path,
        node.index,
      );
    }
    let value: bigint;
    try {
      value = BigInt(node.value);
    } catch {
      throw new BodyParseError('id64_format', 'id64 string is not a valid integer', path, node.index);
    }
    if (value < INT64_MIN || value > INT64_MAX) {
      throw new BodyParseError(
        'id64_overflow',
        'id64 value is outside the signed 64-bit integer range',
        path,
        node.index,
      );
    }
    // BigInt.toString renders a canonical decimal form ("-0" becomes "0").
    return value.toString();
  }

  if (node.kind !== 'number') {
    throw new BodyParseError('type_mismatch', 'id64 field requires a JSON string or integer literal', path, node.index);
  }
  if (!PLAIN_INTEGER_RE.test(node.raw)) {
    throw new BodyParseError(
      'id64_format',
      'id64 numeric literals must be plain decimal integers; exponents and fractions are rejected',
      path,
      node.index,
    );
  }
  const parsed = Number(node.raw);
  if (!Number.isSafeInteger(parsed)) {
    throw new BodyParseError(
      'safeint_overflow',
      'id64 numeric literals must fit in a JavaScript safe integer; send the value as a string for full int64 range',
      path,
      node.index,
    );
  }
  return String(parsed);
}

function convertSafeInt(node: RawNode, path: string): number {
  if (node.kind !== 'number') {
    throw new BodyParseError('type_mismatch', "safeInt field requires an integer literal", path, node.index);
  }
  if (!PLAIN_INTEGER_RE.test(node.raw)) {
    throw new BodyParseError(
      'type_mismatch',
      'safeInt field requires a plain decimal integer without exponent or fraction',
      path,
      node.index,
    );
  }
  const parsed = Number(node.raw);
  if (!Number.isSafeInteger(parsed)) {
    throw new BodyParseError(
      'safeint_overflow',
      'safeInt value must be within the JavaScript safe integer range [-(2^53-1), 2^53-1]',
      path,
      node.index,
    );
  }
  return parsed;
}

function convertNumber(node: RawNode, path: string): number {
  if (node.kind !== 'number') {
    throw new BodyParseError('type_mismatch', "number field requires a numeric literal", path, node.index);
  }
  const parsed = Number(node.raw);
  if (!Number.isFinite(parsed)) {
    throw new BodyParseError(
      'number_non_finite',
      'number field must resolve to a finite value',
      path,
      node.index,
    );
  }
  if (Object.is(parsed, -0)) {
    throw new BodyParseError(
      'negative_zero',
      'negative zero is not accepted',
      path,
      node.index,
    );
  }
  return parsed;
}

function convertUndeclared(node: RawNode, path: string): ParsedValue {
  switch (node.kind) {
    case 'object': {
      const result: Record<string, ParsedValue> = Object.create(null) as Record<string, ParsedValue>;
      for (const entry of node.entries) {
        result[entry.key] =
          convertUndeclared(entry.value, joinPath(path, entry.key));
      }
      return result;
    }
    case 'array':
      return node.items.map((item, index) => convertUndeclared(item, joinPath(path, String(index))));
    case 'string':
      return node.value;
    case 'boolean':
      return node.value;
    case 'null':
      return null;
    case 'number':
      return convertUndeclaredNumber(node.raw, path, node.index);
  }
}

function convertUndeclaredNumber(raw: string, path: string, index: number): number {
  const parsed = Number(raw);
  if (!Number.isFinite(parsed)) {
    throw new BodyParseError(
      'number_non_finite',
      'undeclared numeric field must resolve to a finite value',
      path,
      index,
    );
  }
  if (Number.isInteger(parsed) && !Number.isSafeInteger(parsed)) {
    throw new BodyParseError(
      'unsafe_undeclared_number',
      'undeclared integer fields (including exponent forms such as 1e21) must not exceed the JavaScript safe integer range; declare them explicitly',
      path,
      index,
    );
  }
  if (Object.is(parsed, -0)) {
    throw new BodyParseError('negative_zero', 'negative zero is not accepted', path, index);
  }
  return parsed;
}

function joinPath(path: string, segment: string): string {
  if (path === '') {
    if (/^\d+$/.test(segment)) {
      return `$[${segment}]`;
    }
    return `$.${segment}`;
  }
  if (/^\d+$/.test(segment)) {
    return `${path}[${segment}]`;
  }
  return `${path}.${segment}`;
}
