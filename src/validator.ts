import { stableStringify } from './digest.ts';

/** 一条校验失败。instancePath / schemaPath 均为 RFC 6901 JSON Pointer，根路径为 ""。 */
export interface ValidationError {
  instancePath: string;
  schemaPath: string;
  keyword: string;
  message: string;
}

export interface ValidationResult {
  valid: boolean;
  errors: ValidationError[];
}

/** schema 使用了本实现未承诺支持的关键字（或不支持的形式）。 */
export class UnsupportedKeywordError extends Error {
  readonly keyword: string;
  readonly schemaPath: string;

  constructor(keyword: string, schemaPath: string) {
    super(`unsupported keyword "${keyword}" at schema path "${schemaPath || '/'}"`);
    this.name = 'UnsupportedKeywordError';
    this.keyword = keyword;
    this.schemaPath = schemaPath;
  }
}

/** 注释类关键字：识别但不参与校验。 */
const IGNORED_KEYWORDS = new Set([
  '$schema',
  '$id',
  '$comment',
  'title',
  'description',
  'default',
  'examples',
]);

/** 本实现承诺支持的关键字子集。未在此集合中的关键字一律拒绝。 */
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
  'required',
  'properties',
  'additionalProperties',
  'items',
  'minItems',
  'maxItems',
  'uniqueItems',
  'allOf',
  'anyOf',
  'oneOf',
  'not',
]);

const VALID_TYPES = new Set([
  'null',
  'boolean',
  'number',
  'integer',
  'string',
  'array',
  'object',
]);

export function validate(schema: unknown, instance: unknown): ValidationResult {
  const errors: ValidationError[] = [];
  validateNode(schema, instance, '', '', errors);
  return { valid: errors.length === 0, errors };
}

function validateNode(
  schema: unknown,
  instance: unknown,
  instancePath: string,
  schemaPath: string,
  errors: ValidationError[],
): void {
  if (schema === true) {
    return;
  }
  if (schema === false) {
    errors.push({
      instancePath,
      schemaPath,
      keyword: 'false',
      message: 'boolean schema false rejects every instance',
    });
    return;
  }
  if (!isPlainObject(schema)) {
    throw new Error(`schema at "${schemaPath || '/'}" must be an object or boolean`);
  }

  for (const key of Object.keys(schema)) {
    if (!SUPPORTED_KEYWORDS.has(key) && !IGNORED_KEYWORDS.has(key)) {
      throw new UnsupportedKeywordError(key, joinPointer(schemaPath, key));
    }
  }

  if (schema.type !== undefined) {
    const expected = Array.isArray(schema.type) ? schema.type : [schema.type];
    for (const candidate of expected) {
      if (typeof candidate !== 'string' || !VALID_TYPES.has(candidate)) {
        throw new Error(`invalid type value at ${joinPointer(schemaPath, 'type')}`);
      }
    }
    if (!expected.some((candidate) => matchesType(candidate as string, instance))) {
      errors.push({
        instancePath,
        schemaPath: joinPointer(schemaPath, 'type'),
        keyword: 'type',
        message: `expected type ${expected.join(' | ')}, got ${instanceType(instance)}`,
      });
    }
  }

  if ('const' in schema && !deepEqual(schema.const, instance)) {
    errors.push({
      instancePath,
      schemaPath: joinPointer(schemaPath, 'const'),
      keyword: 'const',
      message: `value must equal ${JSON.stringify(schema.const)}`,
    });
  }

  if (Array.isArray(schema.enum) && !schema.enum.some((candidate) => deepEqual(candidate, instance))) {
    errors.push({
      instancePath,
      schemaPath: joinPointer(schemaPath, 'enum'),
      keyword: 'enum',
      message: `value must be one of ${JSON.stringify(schema.enum)}`,
    });
  }

  if (typeof instance === 'string') {
    const length = Array.from(instance).length;
    if (typeof schema.minLength === 'number' && length < schema.minLength) {
      errors.push({
        instancePath,
        schemaPath: joinPointer(schemaPath, 'minLength'),
        keyword: 'minLength',
        message: `string length ${length} is less than minLength ${schema.minLength}`,
      });
    }
    if (typeof schema.maxLength === 'number' && length > schema.maxLength) {
      errors.push({
        instancePath,
        schemaPath: joinPointer(schemaPath, 'maxLength'),
        keyword: 'maxLength',
        message: `string length ${length} is greater than maxLength ${schema.maxLength}`,
      });
    }
    if (typeof schema.pattern === 'string') {
      let regex: RegExp;
      try {
        regex = new RegExp(schema.pattern);
      } catch {
        throw new Error(`invalid regex at ${joinPointer(schemaPath, 'pattern')}`);
      }
      if (!regex.test(instance)) {
        errors.push({
          instancePath,
          schemaPath: joinPointer(schemaPath, 'pattern'),
          keyword: 'pattern',
          message: `string does not match pattern ${JSON.stringify(schema.pattern)}`,
        });
      }
    }
  }

  if (typeof instance === 'number') {
    if (typeof schema.minimum === 'number' && instance < schema.minimum) {
      errors.push({
        instancePath,
        schemaPath: joinPointer(schemaPath, 'minimum'),
        keyword: 'minimum',
        message: `${instance} is less than minimum ${schema.minimum}`,
      });
    }
    if (typeof schema.maximum === 'number' && instance > schema.maximum) {
      errors.push({
        instancePath,
        schemaPath: joinPointer(schemaPath, 'maximum'),
        keyword: 'maximum',
        message: `${instance} is greater than maximum ${schema.maximum}`,
      });
    }
    if (typeof schema.exclusiveMinimum === 'number' && instance <= schema.exclusiveMinimum) {
      errors.push({
        instancePath,
        schemaPath: joinPointer(schemaPath, 'exclusiveMinimum'),
        keyword: 'exclusiveMinimum',
        message: `${instance} is not greater than exclusiveMinimum ${schema.exclusiveMinimum}`,
      });
    }
    if (typeof schema.exclusiveMaximum === 'number' && instance >= schema.exclusiveMaximum) {
      errors.push({
        instancePath,
        schemaPath: joinPointer(schemaPath, 'exclusiveMaximum'),
        keyword: 'exclusiveMaximum',
        message: `${instance} is not less than exclusiveMaximum ${schema.exclusiveMaximum}`,
      });
    }
    if (typeof schema.multipleOf === 'number' && schema.multipleOf > 0) {
      const ratio = instance / schema.multipleOf;
      if (Math.abs(ratio - Math.round(ratio)) > 1e-9) {
        errors.push({
          instancePath,
          schemaPath: joinPointer(schemaPath, 'multipleOf'),
          keyword: 'multipleOf',
          message: `${instance} is not a multiple of ${schema.multipleOf}`,
        });
      }
    }
  }

  if (isPlainObject(instance)) {
    if (Array.isArray(schema.required)) {
      for (const key of schema.required) {
        if (!(key in instance)) {
          errors.push({
            instancePath,
            schemaPath: joinPointer(schemaPath, 'required'),
            keyword: 'required',
            message: `missing required property "${key}"`,
          });
        }
      }
    }

    const properties = schema.properties;
    if (isPlainObject(properties)) {
      for (const [key, subSchema] of Object.entries(properties)) {
        if (key in instance) {
          validateNode(
            subSchema,
            instance[key],
            joinPointer(instancePath, key),
            joinPointer(joinPointer(schemaPath, 'properties'), key),
            errors,
          );
        }
      }
    }

    if (schema.additionalProperties !== undefined) {
      const declared = isPlainObject(properties) ? new Set(Object.keys(properties)) : new Set<string>();
      for (const key of Object.keys(instance)) {
        if (declared.has(key)) {
          continue;
        }
        if (schema.additionalProperties === false) {
          errors.push({
            instancePath,
            schemaPath: joinPointer(schemaPath, 'additionalProperties'),
            keyword: 'additionalProperties',
            message: `unexpected additional property "${key}"`,
          });
        } else {
          validateNode(
            schema.additionalProperties,
            instance[key],
            joinPointer(instancePath, key),
            joinPointer(schemaPath, 'additionalProperties'),
            errors,
          );
        }
      }
    }
  }

  if (Array.isArray(instance)) {
    if (schema.items !== undefined) {
      if (Array.isArray(schema.items)) {
        throw new UnsupportedKeywordError(
          'items (tuple form)',
          joinPointer(schemaPath, 'items'),
        );
      }
      instance.forEach((item, index) => {
        validateNode(
          schema.items as unknown,
          item,
          joinPointer(instancePath, index),
          joinPointer(schemaPath, 'items'),
          errors,
        );
      });
    }
    if (typeof schema.minItems === 'number' && instance.length < schema.minItems) {
      errors.push({
        instancePath,
        schemaPath: joinPointer(schemaPath, 'minItems'),
        keyword: 'minItems',
        message: `array length ${instance.length} is less than minItems ${schema.minItems}`,
      });
    }
    if (typeof schema.maxItems === 'number' && instance.length > schema.maxItems) {
      errors.push({
        instancePath,
        schemaPath: joinPointer(schemaPath, 'maxItems'),
        keyword: 'maxItems',
        message: `array length ${instance.length} is greater than maxItems ${schema.maxItems}`,
      });
    }
    if (schema.uniqueItems === true) {
      outer: for (let i = 0; i < instance.length; i += 1) {
        for (let j = i + 1; j < instance.length; j += 1) {
          if (deepEqual(instance[i], instance[j])) {
            errors.push({
              instancePath: joinPointer(instancePath, j),
              schemaPath: joinPointer(schemaPath, 'uniqueItems'),
              keyword: 'uniqueItems',
              message: `array items ${i} and ${j} are duplicates`,
            });
            break outer;
          }
        }
      }
    }
  }

  if (Array.isArray(schema.allOf)) {
    schema.allOf.forEach((subSchema, index) => {
      validateNode(
        subSchema,
        instance,
        instancePath,
        joinPointer(joinPointer(schemaPath, 'allOf'), index),
        errors,
      );
    });
  }

  if (Array.isArray(schema.anyOf)) {
    const matched = schema.anyOf.some((subSchema, index) =>
      isValidNode(subSchema, instance, instancePath, joinPointer(joinPointer(schemaPath, 'anyOf'), index)),
    );
    if (!matched) {
      errors.push({
        instancePath,
        schemaPath: joinPointer(schemaPath, 'anyOf'),
        keyword: 'anyOf',
        message: `instance matches none of the ${schema.anyOf.length} anyOf subschemas`,
      });
    }
  }

  if (Array.isArray(schema.oneOf)) {
    let matches = 0;
    schema.oneOf.forEach((subSchema, index) => {
      if (isValidNode(subSchema, instance, instancePath, joinPointer(joinPointer(schemaPath, 'oneOf'), index))) {
        matches += 1;
      }
    });
    if (matches !== 1) {
      errors.push({
        instancePath,
        schemaPath: joinPointer(schemaPath, 'oneOf'),
        keyword: 'oneOf',
        message: `instance matches ${matches} oneOf subschemas, expected exactly 1`,
      });
    }
  }

  if (schema.not !== undefined && isValidNode(schema.not, instance, instancePath, joinPointer(schemaPath, 'not'))) {
    errors.push({
      instancePath,
      schemaPath: joinPointer(schemaPath, 'not'),
      keyword: 'not',
      message: 'instance matches the not subschema',
    });
  }
}

function isValidNode(
  schema: unknown,
  instance: unknown,
  instancePath: string,
  schemaPath: string,
): boolean {
  const errors: ValidationError[] = [];
  validateNode(schema, instance, instancePath, schemaPath, errors);
  return errors.length === 0;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function deepEqual(a: unknown, b: unknown): boolean {
  return stableStringify(a) === stableStringify(b);
}

function escapePointer(segment: string): string {
  return segment.replace(/~/g, '~0').replace(/\//g, '~1');
}

function joinPointer(base: string, segment: string | number): string {
  return `${base}/${escapePointer(String(segment))}`;
}

type JsonType = 'null' | 'boolean' | 'number' | 'integer' | 'string' | 'array' | 'object';

function instanceType(value: unknown): JsonType {
  if (value === null) {
    return 'null';
  }
  if (Array.isArray(value)) {
    return 'array';
  }
  if (typeof value === 'number') {
    return Number.isInteger(value) ? 'integer' : 'number';
  }
  if (typeof value === 'boolean') {
    return 'boolean';
  }
  if (typeof value === 'string') {
    return 'string';
  }
  return 'object';
}

function matchesType(expected: string, value: unknown): boolean {
  const actual = instanceType(value);
  if (expected === actual) {
    return true;
  }
  return expected === 'number' && actual === 'integer';
}
