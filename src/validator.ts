import type { Schema, Segment, ValidationError } from './types.ts';

const escapePointer = (s: string): string => s.replace(/~/g, '~0').replace(/\//g, '~1');

function deepEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== typeof b || a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((v, i) => deepEqual(v, b[i]));
  }
  if (typeof a === 'object') {
    const ao = a as Record<string, unknown>;
    const bo = b as Record<string, unknown>;
    const ka = Object.keys(ao);
    const kb = Object.keys(bo);
    if (ka.length !== kb.length) return false;
    return ka.every((k) => deepEqual(ao[k], bo[k]));
  }
  return false;
}

function typeOf(value: unknown): string {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  if (typeof value === 'number') return Number.isInteger(value) ? 'integer' : 'number';
  return typeof value;
}

function typeMatches(value: unknown, expected: string): boolean {
  const actual = typeOf(value);
  if (actual === expected) return true;
  if (expected === 'number' && actual === 'integer') return true;
  return false;
}

/**
 * Validates `instance` against the supported schema subset (see README).
 * Every error carries both the instance path and the rule (schema) path.
 */
export function validate(instance: unknown, schema: Schema, schemaVersion: string): ValidationError[] {
  const errors: ValidationError[] = [];

  const push = (keyword: string, instancePath: Segment[], schemaPath: string, message: string): void => {
    errors.push({ schemaVersion, keyword, instancePath, schemaPath, message });
  };

  const visit = (value: unknown, sub: Schema, instancePath: Segment[], schemaPath: string): void => {
    if ('const' in sub && !deepEqual(value, sub.const)) {
      push('const', instancePath, `${schemaPath}/const`, `must equal ${JSON.stringify(sub.const)}`);
    }
    if (sub.enum !== undefined && !sub.enum.some((e) => deepEqual(e, value))) {
      push('enum', instancePath, `${schemaPath}/enum`, `must be one of ${JSON.stringify(sub.enum)}`);
    }
    if (sub.type !== undefined) {
      const types = Array.isArray(sub.type) ? sub.type : [sub.type];
      if (!types.some((t) => typeMatches(value, t))) {
        push('type', instancePath, `${schemaPath}/type`, `expected type ${types.join(' or ')}, got ${typeOf(value)}`);
        return;
      }
    }
    if (typeof value === 'number') {
      if (sub.minimum !== undefined && value < sub.minimum) {
        push('minimum', instancePath, `${schemaPath}/minimum`, `must be >= ${sub.minimum}`);
      }
      if (sub.maximum !== undefined && value > sub.maximum) {
        push('maximum', instancePath, `${schemaPath}/maximum`, `must be <= ${sub.maximum}`);
      }
      if (sub.multipleOf !== undefined && value % sub.multipleOf !== 0) {
        push('multipleOf', instancePath, `${schemaPath}/multipleOf`, `must be a multiple of ${sub.multipleOf}`);
      }
    }
    if (typeof value === 'string') {
      if (sub.minLength !== undefined && value.length < sub.minLength) {
        push('minLength', instancePath, `${schemaPath}/minLength`, `must be at least ${sub.minLength} characters`);
      }
      if (sub.maxLength !== undefined && value.length > sub.maxLength) {
        push('maxLength', instancePath, `${schemaPath}/maxLength`, `must be at most ${sub.maxLength} characters`);
      }
      if (sub.pattern !== undefined && !new RegExp(sub.pattern).test(value)) {
        push('pattern', instancePath, `${schemaPath}/pattern`, `must match pattern ${sub.pattern}`);
      }
    }
    if (Array.isArray(value)) {
      if (sub.minItems !== undefined && value.length < sub.minItems) {
        push('minItems', instancePath, `${schemaPath}/minItems`, `must have at least ${sub.minItems} items`);
      }
      if (sub.maxItems !== undefined && value.length > sub.maxItems) {
        push('maxItems', instancePath, `${schemaPath}/maxItems`, `must have at most ${sub.maxItems} items`);
      }
      if (sub.items !== undefined) {
        const itemSchema = sub.items;
        value.forEach((item, i) => visit(item, itemSchema, [...instancePath, i], `${schemaPath}/items`));
      }
    }
    if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
      const obj = value as Record<string, unknown>;
      if (sub.required !== undefined) {
        for (const key of sub.required) {
          if (!(key in obj)) {
            push('required', instancePath, `${schemaPath}/required`, `missing required property "${key}"`);
          }
        }
      }
      if (sub.properties !== undefined) {
        for (const [key, propSchema] of Object.entries(sub.properties)) {
          if (key in obj) {
            visit(obj[key], propSchema, [...instancePath, key], `${schemaPath}/properties/${escapePointer(key)}`);
          }
        }
      }
      if (sub.additionalProperties === false && sub.properties !== undefined) {
        for (const key of Object.keys(obj)) {
          if (!(key in sub.properties)) {
            push('additionalProperties', [...instancePath, key], `${schemaPath}/additionalProperties`, `additional property "${key}" is not allowed`);
          }
        }
      }
    }
  };

  visit(instance, schema, [], '#');
  return errors;
}
