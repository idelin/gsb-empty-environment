/**
 * Hand-written JSON Schema subset validator (no external validator).
 *
 * Implemented keywords:
 *   - type            "null" | "boolean" | "object" | "array" | "string" |
 *                     "number" | "integer", or an array of those
 *   - enum            exact-value list (numbers compared numerically)
 *   - const           exact value (numbers compared numerically)
 *   - properties      per-property subschemas
 *   - required        required property names
 *   - additionalProperties  false | true | subschema
 *   - items           subschema for every array element
 *   - minItems / maxItems
 *   - minLength / maxLength
 *   - pattern         RegExp tested against strings
 *   - minimum / maximum / exclusiveMinimum / exclusiveMaximum
 *                     exact decimal comparison; bounds may be given as
 *                     number, bigint, numeric string or Decimal
 *
 * Anything outside this subset is reported in `warnings` and ignored.
 * Every error carries both an instance path (JSON Pointer into the data) and
 * a rule path (JSON Pointer into the schema, e.g. /properties/age/minimum).
 */

import { compareDecimal, Decimal, DecimalError, toDecimal } from './decimal.ts';
import type { PrecisionBudget } from './decimal.ts';
import type { JsonValue } from './parser.ts';

export interface SchemaError {
  readonly keyword: string;
  readonly instancePath: string;
  readonly schemaPath: string;
  readonly message: string;
}

export interface ValidationResult {
  readonly valid: boolean;
  readonly errors: SchemaError[];
  readonly warnings: string[];
}

interface Context {
  errors: SchemaError[];
  warnings: string[];
  budget?: PrecisionBudget;
}

const SUPPORTED_KEYWORDS = new Set<string>([
  '$id', '$schema', '$ref', '$comment', 'title', 'description', 'default', 'examples',
  'type', 'enum', 'const',
  'properties', 'required', 'additionalProperties',
  'items', 'minItems', 'maxItems',
  'minLength', 'maxLength', 'pattern',
  'minimum', 'maximum', 'exclusiveMinimum', 'exclusiveMaximum',
]);

const KNOWN_TYPES = new Set([
  'null', 'boolean', 'object', 'array', 'string', 'number', 'integer',
]);

export function validate(
  value: JsonValue,
  schema: unknown,
  options: { budget?: PrecisionBudget } = {},
): ValidationResult {
  const context: Context = { errors: [], warnings: [], budget: options.budget };
  walk(value, schema, '', '', context);
  return { valid: context.errors.length === 0, errors: context.errors, warnings: context.warnings };
}

function pointer(token: string | number): string {
  return '/' + String(token).replace(/~/g, '~0').replace(/\//g, '~1');
}

function report(
  context: Context,
  keyword: string,
  instancePath: string,
  schemaPath: string,
  message: string,
): void {
  context.errors.push({ keyword, instancePath, schemaPath, message });
}

function isPlainObject(value: unknown): value is { [key: string]: unknown } {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isDecimal(value: JsonValue): value is Decimal {
  return value instanceof Decimal;
}

function walk(
  value: JsonValue,
  schema: unknown,
  instancePath: string,
  schemaPath: string,
  context: Context,
): void {
  if (!isPlainObject(schema)) {
    if (typeof schema === 'boolean') {
      // boolean schemas: false always fails, true always passes
      if (schema === false) {
        report(context, 'false-schema', instancePath, schemaPath, 'value rejected by `false` schema');
      }
      return;
    }
    context.warnings.push(`schema at ${schemaPath || '/'} is not an object; skipped`);
    return;
  }

  for (const keyword of Object.keys(schema)) {
    if (!SUPPORTED_KEYWORDS.has(keyword) && !keyword.startsWith('x-')) {
      context.warnings.push(
        `unsupported schema keyword "${keyword}" at ${schemaPath + pointer(keyword)} is ignored`,
      );
    }
  }

  if (schema.const !== undefined && !jsonEquals(value, schema.const)) {
    report(context, 'const', instancePath, schemaPath + pointer('const'), 'value does not equal const');
  }

  if (schema.enum !== undefined) {
    if (!Array.isArray(schema.enum)) {
      context.warnings.push(`"enum" at ${schemaPath + pointer('enum')} must be an array; skipped`);
    } else if (!schema.enum.some((candidate) => jsonEquals(value, candidate))) {
      report(context, 'enum', instancePath, schemaPath + pointer('enum'), 'value is not one of enum');
    }
  }

  if (schema.type !== undefined) {
    checkType(value, schema.type, instancePath, schemaPath, context);
  }

  if (isDecimal(value)) {
    checkNumberBounds(value, schema, instancePath, schemaPath, context);
  }

  if (typeof value === 'string') {
    checkStringLength(value, schema, instancePath, schemaPath, context);
    if (schema.pattern !== undefined) {
      let regex: RegExp;
      try {
        regex = new RegExp(schema.pattern);
      } catch {
        context.warnings.push(`invalid "pattern" regex at ${schemaPath + pointer('pattern')}; skipped`);
        return;
      }
      if (!regex.test(value)) {
        report(context, 'pattern', instancePath, schemaPath + pointer('pattern'),
          `string does not match pattern ${JSON.stringify(schema.pattern)}`);
      }
    }
  }

  if (Array.isArray(value)) {
    checkArray(value, schema, instancePath, schemaPath, context);
  } else if (isPlainObject(value)) {
    checkObject(value, schema, instancePath, schemaPath, context);
  }
}

function checkType(
  value: JsonValue,
  typeRule: unknown,
  instancePath: string,
  schemaPath: string,
  context: Context,
): void {
  const types = Array.isArray(typeRule) ? typeRule : [typeRule];
  const path = schemaPath + pointer('type');
  for (const type of types) {
    if (typeof type !== 'string' || !KNOWN_TYPES.has(type)) {
      context.warnings.push(`unknown type ${JSON.stringify(type)} at ${path}; skipped`);
      return;
    }
  }
  if (types.some((type) => matchesType(value, type as string))) return;
  report(context, 'type', instancePath, path,
    `expected type ${types.join(' | ')} but got ${actualType(value)}`);
}

function matchesType(value: JsonValue, type: string): boolean {
  switch (type) {
    case 'null': return value === null;
    case 'boolean': return typeof value === 'boolean';
    case 'string': return typeof value === 'string';
    case 'array': return Array.isArray(value);
    case 'object': return isPlainObject(value);
    case 'number': return value instanceof Decimal;
    case 'integer': return value instanceof Decimal && value.isInteger();
    default: return false;
  }
}

function actualType(value: JsonValue): string {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  if (value instanceof Decimal) return 'number';
  return typeof value;
}

function boundDecimal(schema: { [key: string]: unknown }, keyword: string,
                      schemaPath: string, context: Context): Decimal | undefined {
  if (schema[keyword] === undefined) return undefined;
  try {
    return toDecimal(schema[keyword] as Decimal | number | bigint | string, context.budget);
  } catch (error) {
    context.warnings.push(
      `"${keyword}" at ${schemaPath + pointer(keyword)} is not a finite numeric value ` +
      `(${(error as DecimalError).message}); skipped`,
    );
    return undefined;
  }
}

function checkNumberBounds(
  value: Decimal,
  schema: { [key: string]: unknown },
  instancePath: string,
  schemaPath: string,
  context: Context,
): void {
  const minimum = boundDecimal(schema, 'minimum', schemaPath, context);
  if (minimum !== undefined && compareDecimal(value, minimum) < 0) {
    report(context, 'minimum', instancePath, schemaPath + pointer('minimum'),
      `${value.signature()} must be >= ${minimum.signature()}`);
  }
  const maximum = boundDecimal(schema, 'maximum', schemaPath, context);
  if (maximum !== undefined && compareDecimal(value, maximum) > 0) {
    report(context, 'maximum', instancePath, schemaPath + pointer('maximum'),
      `${value.signature()} must be <= ${maximum.signature()}`);
  }
  const exclusiveMinimum = boundDecimal(schema, 'exclusiveMinimum', schemaPath, context);
  if (exclusiveMinimum !== undefined && compareDecimal(value, exclusiveMinimum) <= 0) {
    report(context, 'exclusiveMinimum', instancePath, schemaPath + pointer('exclusiveMinimum'),
      `${value.signature()} must be > ${exclusiveMinimum.signature()}`);
  }
  const exclusiveMaximum = boundDecimal(schema, 'exclusiveMaximum', schemaPath, context);
  if (exclusiveMaximum !== undefined && compareDecimal(value, exclusiveMaximum) >= 0) {
    report(context, 'exclusiveMaximum', instancePath, schemaPath + pointer('exclusiveMaximum'),
      `${value.signature()} must be < ${exclusiveMaximum.signature()}`);
  }
}

function checkStringLength(
  value: string,
  schema: { [key: string]: unknown },
  instancePath: string,
  schemaPath: string,
  context: Context,
): void {
  if (typeof schema.minLength === 'number' && [...value].length < schema.minLength) {
    report(context, 'minLength', instancePath, schemaPath + pointer('minLength'),
      `string length must be >= ${schema.minLength}`);
  }
  if (typeof schema.maxLength === 'number' && [...value].length > schema.maxLength) {
    report(context, 'maxLength', instancePath, schemaPath + pointer('maxLength'),
      `string length must be <= ${schema.maxLength}`);
  }
}

function checkArray(
  value: JsonValue[],
  schema: { [key: string]: unknown },
  instancePath: string,
  schemaPath: string,
  context: Context,
): void {
  if (typeof schema.minItems === 'number' && value.length < schema.minItems) {
    report(context, 'minItems', instancePath, schemaPath + pointer('minItems'),
      `array length must be >= ${schema.minItems}`);
  }
  if (typeof schema.maxItems === 'number' && value.length > schema.maxItems) {
    report(context, 'maxItems', instancePath, schemaPath + pointer('maxItems'),
      `array length must be <= ${schema.maxItems}`);
  }
  if (schema.items !== undefined) {
    value.forEach((item, index) => {
      walk(item, schema.items, instancePath + pointer(index), schemaPath + pointer('items'), context);
    });
  }
}

function checkObject(
  value: { [key: string]: JsonValue },
  schema: { [key: string]: unknown },
  instancePath: string,
  schemaPath: string,
  context: Context,
): void {
  if (Array.isArray(schema.required)) {
    for (const required of schema.required) {
      if (typeof required !== 'string') continue;
      if (!Object.prototype.hasOwnProperty.call(value, required)) {
        report(context, 'required', instancePath, schemaPath + pointer('required'),
          `missing required property ${JSON.stringify(required)}`);
      }
    }
  }

  const properties = isPlainObject(schema.properties) ? schema.properties : null;
  const additionalProperties =
    schema.additionalProperties === undefined ? true : schema.additionalProperties;

  for (const [key, child] of Object.entries(value)) {
    if (properties !== null && key in properties) {
      walk(child, properties[key], instancePath + pointer(key),
        schemaPath + pointer('properties') + pointer(key), context);
    } else if (additionalProperties === false) {
      report(context, 'additionalProperties', instancePath + pointer(key),
        schemaPath + pointer('additionalProperties'),
        `additional property ${JSON.stringify(key)} is not allowed`);
    } else if (isPlainObject(additionalProperties)) {
      walk(child, additionalProperties, instancePath + pointer(key),
        schemaPath + pointer('additionalProperties'), context);
    }
  }
}

/**
 * Structural equality against plain schema values (as produced by parsing a
 * schema). Numbers in the compared instance are exact Decimals and compare
 * numerically to numbers / numeric strings on the schema side, so enum/const
 * treat 100, 1e2 and 100.0 as the same value.
 */
function jsonEquals(instance: JsonValue, schemaValue: unknown): boolean {
  if (instance instanceof Decimal) {
    if (schemaValue instanceof Decimal) return compareDecimal(instance, schemaValue) === 0;
    if (typeof schemaValue === 'number' || typeof schemaValue === 'string' ||
        typeof schemaValue === 'bigint') {
      try {
        return compareDecimal(instance, toDecimal(schemaValue)) === 0;
      } catch {
        return false;
      }
    }
    return false;
  }
  if (typeof instance === 'string' || typeof instance === 'boolean') {
    return instance === schemaValue;
  }
  if (instance === null) return schemaValue === null;
  if (Array.isArray(instance)) {
    return Array.isArray(schemaValue) &&
      instance.length === schemaValue.length &&
      instance.every((item, index) => jsonEquals(item, schemaValue[index]));
  }
  if (typeof instance === 'object') {
    if (!isPlainObject(schemaValue)) return false;
    const aKeys = Object.keys(instance);
    const bKeys = Object.keys(schemaValue);
    return aKeys.length === bKeys.length &&
      aKeys.every((key) =>
        Object.prototype.hasOwnProperty.call(schemaValue, key) &&
        jsonEquals(instance[key], schemaValue[key]));
  }
  return false;
}
