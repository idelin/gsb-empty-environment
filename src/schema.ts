/**
 * Hand-written validator for a declared JSON Schema subset -- nothing more
 * is promised. Unsupported keywords are rejected up front (SchemaError)
 * instead of being silently ignored. Every validation failure reports both
 * the instance location (instancePath) and the rule location (schemaPath)
 * as JSON Pointers.
 *
 * Supported keywords:
 *   type, properties, required, additionalProperties (boolean), items,
 *   enum, const, minimum, maximum, exclusiveMinimum, exclusiveMaximum,
 *   multipleOf, minLength, maxLength, minItems, maxItems,
 *   minProperties, maxProperties
 * Annotation keywords $schema/$id/title/description are accepted and ignored.
 * Numbers are compared as exact decimals (no float rounding).
 */

import {
  compareDecimal,
  canonicalDecimal,
  isIntegerDecimal,
  multipleOfDecimal,
} from './decimal.ts';
import { isJsonNumber } from './incremental.ts';
import type { Decimal } from './decimal.ts';
import type { JsonValue } from './incremental.ts';

export class SchemaError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SchemaError';
  }
}

export interface ValidationError {
  /** JSON Pointer into the instance, e.g. "/a/0". */
  readonly instancePath: string;
  /** JSON Pointer into the schema, e.g. "/properties/a/items/minimum". */
  readonly schemaPath: string;
  readonly keyword: string;
  readonly message: string;
}

const TYPES = new Set(['null', 'boolean', 'string', 'number', 'integer', 'array', 'object']);
const ANNOTATIONS = new Set(['$schema', '$id', 'title', 'description']);
const KEYWORDS = new Set([
  ...ANNOTATIONS,
  'type', 'properties', 'required', 'additionalProperties', 'items',
  'enum', 'const',
  'minimum', 'maximum', 'exclusiveMinimum', 'exclusiveMaximum', 'multipleOf',
  'minLength', 'maxLength', 'minItems', 'maxItems', 'minProperties', 'maxProperties',
]);

const esc = (s: string): string => s.replace(/~/g, '~0').replace(/\//g, '~1');

function isPlainObject(v: JsonValue): v is { [k: string]: JsonValue } {
  return typeof v === 'object' && v !== null && !Array.isArray(v) && !isJsonNumber(v);
}

function decimalOf(v: JsonValue | undefined): Decimal | null {
  return v !== undefined && isJsonNumber(v) ? v.decimal : null;
}

/** Validate that a schema stays inside the supported subset. */
export function assertSchema(schema: JsonValue, path: string = ''): void {
  const at = path === '' ? '/' : path;
  if (!isPlainObject(schema)) {
    throw new SchemaError(`schema at ${at} must be an object`);
  }
  for (const key of Object.keys(schema)) {
    if (!KEYWORDS.has(key)) {
      throw new SchemaError(
        `unsupported schema keyword "${key}" at ${at} (supported: ${[...KEYWORDS].join(', ')})`,
      );
    }
  }
  const sub = (keyword: string): void => {
    const v = schema[keyword];
    if (v !== undefined) assertSchema(v, `${path}/${keyword}`);
  };
  if (schema.type !== undefined) {
    const ts = Array.isArray(schema.type) ? schema.type : [schema.type];
    if (ts.length === 0 || ts.some((t) => typeof t !== 'string' || !TYPES.has(t))) {
      throw new SchemaError(`"type" at ${at} must be one of ${[...TYPES].join(', ')}`);
    }
  }
  if (schema.properties !== undefined) {
    if (!isPlainObject(schema.properties)) throw new SchemaError(`"properties" at ${at} must be an object`);
    for (const [k, v] of Object.entries(schema.properties)) {
      assertSchema(v, `${path}/properties/${esc(k)}`);
    }
  }
  if (schema.required !== undefined) {
    if (!Array.isArray(schema.required) || schema.required.some((r) => typeof r !== 'string')) {
      throw new SchemaError(`"required" at ${at} must be an array of strings`);
    }
  }
  if (schema.additionalProperties !== undefined && typeof schema.additionalProperties !== 'boolean') {
    throw new SchemaError(`"additionalProperties" at ${at} must be a boolean in this subset`);
  }
  if (schema.items !== undefined) sub('items');
  if (schema.enum !== undefined && (!Array.isArray(schema.enum) || schema.enum.length === 0)) {
    throw new SchemaError(`"enum" at ${at} must be a non-empty array`);
  }
  for (const k of ['minimum', 'maximum', 'exclusiveMinimum', 'exclusiveMaximum'] as const) {
    if (schema[k] !== undefined && decimalOf(schema[k]) === null) {
      throw new SchemaError(`"${k}" at ${at} must be a number`);
    }
  }
  if (schema.multipleOf !== undefined) {
    const d = decimalOf(schema.multipleOf);
    if (d === null || d.digits === '0' || d.neg) {
      throw new SchemaError(`"multipleOf" at ${at} must be a positive number`);
    }
  }
  for (const k of ['minLength', 'maxLength', 'minItems', 'maxItems', 'minProperties', 'maxProperties'] as const) {
    const d = decimalOf(schema[k]);
    if (schema[k] !== undefined && (d === null || d.neg || !isIntegerDecimal(d))) {
      throw new SchemaError(`"${k}" at ${at} must be a non-negative integer`);
    }
  }
}

function typeOf(v: JsonValue): string {
  if (v === null) return 'null';
  if (typeof v === 'boolean') return 'boolean';
  if (typeof v === 'string') return 'string';
  if (Array.isArray(v)) return 'array';
  if (isJsonNumber(v)) return isIntegerDecimal(v.decimal) ? 'integer' : 'number';
  return 'object';
}

function matchesType(v: JsonValue, t: string): boolean {
  if (t === 'integer') return isJsonNumber(v) && isIntegerDecimal(v.decimal);
  if (t === 'number') return isJsonNumber(v);
  return typeOf(v) === t;
}

function deepEqual(a: JsonValue, b: JsonValue): boolean {
  const an = isJsonNumber(a);
  const bn = isJsonNumber(b);
  if (an || bn) return an && bn && compareDecimal(a.decimal, b.decimal) === 0;
  if (a === null || b === null) return a === b;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((x, i) => deepEqual(x, b[i]));
  }
  if (typeof a === 'object' && typeof b === 'object') {
    const ka = Object.keys(a);
    const kb = Object.keys(b);
    if (ka.length !== kb.length) return false;
    return ka.every((k) => k in b && deepEqual(a[k], b[k]));
  }
  return a === b;
}

function fmt(v: JsonValue): string {
  if (isJsonNumber(v)) return canonicalDecimal(v.decimal);
  if (typeof v === 'string') return JSON.stringify(v);
  if (v === null) return 'null';
  if (typeof v === 'boolean') return String(v);
  return Array.isArray(v) ? '[...]' : '{...}';
}

export function validateJson(instance: JsonValue, schema: JsonValue): ValidationError[] {
  assertSchema(schema);
  const errors: ValidationError[] = [];
  walk(instance, schema as { [k: string]: JsonValue }, '', '', errors);
  return errors;
}

function walk(
  inst: JsonValue,
  schema: { [k: string]: JsonValue },
  ip: string,
  sp: string,
  errors: ValidationError[],
): void {
  const push = (keyword: string, message: string, ipOverride?: string): void => {
    errors.push({ instancePath: ipOverride ?? ip, schemaPath: `${sp}/${keyword}`, keyword, message });
  };

  if (schema.const !== undefined && !deepEqual(inst, schema.const)) {
    push('const', `value must equal ${fmt(schema.const)}`);
  }
  if (schema.enum !== undefined) {
    const options = schema.enum as JsonValue[];
    if (!options.some((o) => deepEqual(inst, o))) {
      push('enum', 'value is not one of the allowed enum values');
    }
  }
  if (schema.type !== undefined) {
    const ts = (Array.isArray(schema.type) ? schema.type : [schema.type]) as string[];
    if (!ts.some((t) => matchesType(inst, t))) {
      push('type', `expected type ${ts.join(' | ')}, got ${typeOf(inst)}`);
      return; // further keyword checks would cascade noise
    }
  }

  if (isJsonNumber(inst)) {
    const d = inst.decimal;
    const num = (k: string): Decimal | null => decimalOf(schema[k]);
    const min = num('minimum');
    if (min && compareDecimal(d, min) < 0) push('minimum', `${canonicalDecimal(d)} < minimum ${canonicalDecimal(min)}`);
    const max = num('maximum');
    if (max && compareDecimal(d, max) > 0) push('maximum', `${canonicalDecimal(d)} > maximum ${canonicalDecimal(max)}`);
    const exMin = num('exclusiveMinimum');
    if (exMin && compareDecimal(d, exMin) <= 0) push('exclusiveMinimum', `${canonicalDecimal(d)} <= exclusiveMinimum ${canonicalDecimal(exMin)}`);
    const exMax = num('exclusiveMaximum');
    if (exMax && compareDecimal(d, exMax) >= 0) push('exclusiveMaximum', `${canonicalDecimal(d)} >= exclusiveMaximum ${canonicalDecimal(exMax)}`);
    const mul = num('multipleOf');
    if (mul && !multipleOfDecimal(d, mul)) push('multipleOf', `${canonicalDecimal(d)} is not a multiple of ${canonicalDecimal(mul)}`);
  }

  if (typeof inst === 'string') {
    const len = [...inst].length;
    const minL = decimalOf(schema.minLength);
    if (minL && len < Number(canonicalDecimal(minL))) push('minLength', `string length ${len} < minLength ${canonicalDecimal(minL)}`);
    const maxL = decimalOf(schema.maxLength);
    if (maxL && len > Number(canonicalDecimal(maxL))) push('maxLength', `string length ${len} > maxLength ${canonicalDecimal(maxL)}`);
  }

  if (Array.isArray(inst)) {
    const minI = decimalOf(schema.minItems);
    if (minI && inst.length < Number(canonicalDecimal(minI))) push('minItems', `array size ${inst.length} < minItems ${canonicalDecimal(minI)}`);
    const maxI = decimalOf(schema.maxItems);
    if (maxI && inst.length > Number(canonicalDecimal(maxI))) push('maxItems', `array size ${inst.length} > maxItems ${canonicalDecimal(maxI)}`);
    if (schema.items !== undefined) {
      const items = schema.items as { [k: string]: JsonValue };
      inst.forEach((item, i) => walk(item, items, `${ip}/${i}`, `${sp}/items`, errors));
    }
  }

  if (isPlainObject(inst)) {
    const keys = Object.keys(inst);
    const minP = decimalOf(schema.minProperties);
    if (minP && keys.length < Number(canonicalDecimal(minP))) push('minProperties', `property count ${keys.length} < minProperties ${canonicalDecimal(minP)}`);
    const maxP = decimalOf(schema.maxProperties);
    if (maxP && keys.length > Number(canonicalDecimal(maxP))) push('maxProperties', `property count ${keys.length} > maxProperties ${canonicalDecimal(maxP)}`);
    if (Array.isArray(schema.required)) {
      for (const name of schema.required as string[]) {
        if (!(name in inst)) push('required', `missing required property "${name}"`);
      }
    }
    const props = isPlainObject(schema.properties) ? schema.properties : {};
    for (const [k, v] of Object.entries(props)) {
      if (k in inst) {
        walk(inst[k], v as { [k: string]: JsonValue }, `${ip}/${esc(k)}`, `${sp}/properties/${esc(k)}`, errors);
      }
    }
    if (schema.additionalProperties === false) {
      for (const k of keys) {
        if (!(k in props)) {
          push('additionalProperties', `additional property "${k}" is not allowed`, `${ip}/${esc(k)}`);
        }
      }
    }
  }
}
