/**
 * 本工具承诺支持的 JSON Schema 子集（除此之外一概拒绝，不静默忽略）：
 *
 * - 通用: type（单一类型，不支持联合）、enum、const
 * - string: minLength、maxLength、pattern（仅 ^...$ 锚定、字符类/范围/取反、
 *   转义 \d \w \s、量词 * + ? {m} {m,} {m,n}；不支持分组与 alternation）
 * - number/integer: minimum、maximum、exclusiveMinimum、exclusiveMaximum、multipleOf
 * - array: items（单一 schema）、minItems、maxItems、uniqueItems
 * - object: properties、required、additionalProperties（仅布尔值）、
 *   dependentRequired（属性联动）
 *
 * 明确不支持：$ref、allOf/anyOf/oneOf/not、if/then/else、format、prefixItems、
 * contains、propertyNames、patternProperties、minProperties/maxProperties、
 * dependentSchemas、联合 type 等。
 */
export type JsonTypeName =
  | 'string'
  | 'number'
  | 'integer'
  | 'boolean'
  | 'null'
  | 'array'
  | 'object';

export interface Schema {
  type?: JsonTypeName;
  enum?: unknown[];
  const?: unknown;
  // string
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  // number / integer
  minimum?: number;
  maximum?: number;
  exclusiveMinimum?: number;
  exclusiveMaximum?: number;
  multipleOf?: number;
  // array
  items?: Schema;
  minItems?: number;
  maxItems?: number;
  uniqueItems?: boolean;
  // object
  properties?: Record<string, Schema>;
  required?: string[];
  additionalProperties?: boolean;
  dependentRequired?: Record<string, string[]>;
}

export class UnsupportedSchemaError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'UnsupportedSchemaError';
  }
}

export class InvalidSchemaError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'InvalidSchemaError';
  }
}

const SUPPORTED_KEYWORDS = new Set([
  'type',
  'enum',
  'const',
  'minLength',
  'maxLength',
  'pattern',
  'minimum',
  'maximum',
  'exclusiveMinimum',
  'exclusiveMaximum',
  'multipleOf',
  'items',
  'minItems',
  'maxItems',
  'uniqueItems',
  'properties',
  'required',
  'additionalProperties',
  'dependentRequired',
]);

const TYPE_NAMES = new Set([
  'string',
  'number',
  'integer',
  'boolean',
  'null',
  'array',
  'object',
]);

/** 递归校验 schema 本身落在承诺的子集内，否则抛 UnsupportedSchemaError / InvalidSchemaError。 */
export function assertSupported(schema: Schema, path = '#'): void {
  if (schema === null || typeof schema !== 'object' || Array.isArray(schema)) {
    throw new InvalidSchemaError(`${path}: schema 必须是对象`);
  }
  for (const key of Object.keys(schema)) {
    if (!SUPPORTED_KEYWORDS.has(key)) {
      throw new UnsupportedSchemaError(
        `${path}: 不支持的关键字 "${key}"（本工具只承诺 README 中列出的子集）`,
      );
    }
  }
  if (schema.type !== undefined && !TYPE_NAMES.has(schema.type)) {
    throw new InvalidSchemaError(`${path}/type: 未知类型 "${schema.type}"`);
  }
  if (schema.enum !== undefined && !Array.isArray(schema.enum)) {
    throw new InvalidSchemaError(`${path}/enum: 必须是数组`);
  }
  if (schema.multipleOf !== undefined && !(schema.multipleOf > 0)) {
    throw new InvalidSchemaError(`${path}/multipleOf: 必须是正数`);
  }
  for (const name of ['minLength', 'maxLength', 'minItems', 'maxItems'] as const) {
    const v = schema[name];
    if (v !== undefined && (!Number.isInteger(v) || v < 0)) {
      throw new InvalidSchemaError(`${path}/${name}: 必须是非负整数`);
    }
  }
  if (
    schema.additionalProperties !== undefined &&
    typeof schema.additionalProperties !== 'boolean'
  ) {
    throw new UnsupportedSchemaError(
      `${path}/additionalProperties: 仅支持布尔值，不支持子 schema`,
    );
  }
  if (schema.required !== undefined) {
    if (
      !Array.isArray(schema.required) ||
      schema.required.some((r) => typeof r !== 'string')
    ) {
      throw new InvalidSchemaError(`${path}/required: 必须是字符串数组`);
    }
  }
  if (schema.properties !== undefined) {
    for (const [key, sub] of Object.entries(schema.properties)) {
      assertSupported(sub, `${path}/properties/${escapePointer(key)}`);
    }
  }
  if (schema.items !== undefined) {
    assertSupported(schema.items, `${path}/items`);
  }
  if (schema.dependentRequired !== undefined) {
    for (const [trigger, deps] of Object.entries(schema.dependentRequired)) {
      if (!Array.isArray(deps) || deps.some((d) => typeof d !== 'string')) {
        throw new InvalidSchemaError(
          `${path}/dependentRequired/${escapePointer(trigger)}: 必须是字符串数组`,
        );
      }
    }
  }
}

/** JSON Pointer 段转义（RFC 6901）。 */
export function escapePointer(segment: string): string {
  return segment.replace(/~/g, '~0').replace(/\//g, '~1');
}

export function unescapePointer(segment: string): string {
  return segment.replace(/~1/g, '/').replace(/~0/g, '~');
}
