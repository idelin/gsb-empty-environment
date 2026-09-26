import { escapeSegment } from './evidence.ts';

export interface Schema {
  type?: string | string[];
  properties?: Record<string, Schema>;
  required?: string[];
  items?: Schema;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
}

export interface ValidationError {
  instancePath: string;
  rulePath: string;
  keyword: string;
  message: string;
}

export const IMPLEMENTED_KEYWORDS = [
  'type',
  'properties',
  'required',
  'items',
  'minimum',
  'maximum',
  'minLength',
  'maxLength',
] as const;

function typeOf(value: unknown): string {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  if (typeof value === 'number') return Number.isInteger(value) ? 'integer' : 'number';
  return typeof value;
}

function matchesType(value: unknown, expected: string): boolean {
  if (expected === 'number') return typeof value === 'number';
  if (expected === 'integer') return typeof value === 'number' && Number.isInteger(value as number);
  return typeOf(value) === expected;
}

export function validate(instance: unknown, schema: Schema, instancePath = '', rulePath = '#'): ValidationError[] {
  const errors: ValidationError[] = [];

  if (schema.type !== undefined) {
    const allowed = Array.isArray(schema.type) ? schema.type : [schema.type];
    if (!allowed.some((t) => matchesType(instance, t))) {
      errors.push({
        instancePath,
        rulePath: rulePath + '/type',
        keyword: 'type',
        message: `expected type ${allowed.join(' | ')}, got ${typeOf(instance)}`,
      });
      return errors;
    }
  }

  if (typeof instance === 'number') {
    if (schema.minimum !== undefined && instance < schema.minimum) {
      errors.push({
        instancePath,
        rulePath: rulePath + '/minimum',
        keyword: 'minimum',
        message: `${instance} is less than minimum ${schema.minimum}`,
      });
    }
    if (schema.maximum !== undefined && instance > schema.maximum) {
      errors.push({
        instancePath,
        rulePath: rulePath + '/maximum',
        keyword: 'maximum',
        message: `${instance} is greater than maximum ${schema.maximum}`,
      });
    }
  }

  if (typeof instance === 'string') {
    if (schema.minLength !== undefined && instance.length < schema.minLength) {
      errors.push({
        instancePath,
        rulePath: rulePath + '/minLength',
        keyword: 'minLength',
        message: `string length ${instance.length} is less than minLength ${schema.minLength}`,
      });
    }
    if (schema.maxLength !== undefined && instance.length > schema.maxLength) {
      errors.push({
        instancePath,
        rulePath: rulePath + '/maxLength',
        keyword: 'maxLength',
        message: `string length ${instance.length} is greater than maxLength ${schema.maxLength}`,
      });
    }
  }

  if (Array.isArray(instance) && schema.items !== undefined) {
    instance.forEach((item, index) => {
      errors.push(...validate(item, schema.items as Schema, `${instancePath}/${index}`, rulePath + '/items'));
    });
  }

  if (instance !== null && typeof instance === 'object' && !Array.isArray(instance)) {
    const obj = instance as Record<string, unknown>;
    for (const key of schema.required ?? []) {
      if (!Object.prototype.hasOwnProperty.call(obj, key)) {
        errors.push({
          instancePath,
          rulePath: rulePath + '/required',
          keyword: 'required',
          message: `missing required property "${key}"`,
        });
      }
    }
    for (const [key, subschema] of Object.entries(schema.properties ?? {})) {
      if (Object.prototype.hasOwnProperty.call(obj, key)) {
        const seg = escapeSegment(key);
        errors.push(...validate(obj[key], subschema, `${instancePath}/${seg}`, `${rulePath}/properties/${seg}`));
      }
    }
  }

  return errors;
}
