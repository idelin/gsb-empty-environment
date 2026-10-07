/**
 * Schema model: finite enums, nullable/required fields, nested objects,
 * and arrays with length bounds. Objects are closed (unknown keys rejected).
 */

export type Schema =
  | EnumSchema
  | ObjectSchema
  | ArraySchema;

export interface EnumSchema {
  kind: 'enum';
  /** Finite domain; order defines minimality (index 0 is the smallest value). */
  values: readonly string[];
}

export interface ObjectSchema {
  kind: 'object';
  fields: Record<string, Field>;
}

export interface ArraySchema {
  kind: 'array';
  items: Schema;
  /** Inclusive lower bound on length; defaults to 0. */
  minLength?: number;
  /** Inclusive upper bound on length; defaults to Infinity. */
  maxLength?: number;
}

export interface Field {
  schema: Schema;
  /** Absent from a record when false. Defaults to false. */
  required?: boolean;
  /** May hold null when present (or when required). Defaults to false. */
  nullable?: boolean;
}

export type JsonValue = null | string | JsonValue[] | { [key: string]: JsonValue };

const isPlainObject = (v: unknown): v is Record<string, unknown> =>
  typeof v === 'object' && v !== null && !Array.isArray(v);

/** Validate a record against a schema. */
export function validate(schema: Schema, value: unknown): boolean {
  switch (schema.kind) {
    case 'enum':
      return typeof value === 'string' && schema.values.includes(value);
    case 'array': {
      if (!Array.isArray(value)) return false;
      const min = schema.minLength ?? 0;
      const max = schema.maxLength ?? Infinity;
      if (value.length < min || value.length > max) return false;
      return value.every((item) => validate(schema.items, item));
    }
    case 'object': {
      if (!isPlainObject(value)) return false;
      for (const [name, field] of Object.entries(schema.fields)) {
        const has = Object.prototype.hasOwnProperty.call(value, name);
        if (!has) {
          if (field.required) return false;
          continue;
        }
        const v = (value as Record<string, unknown>)[name];
        if (v === null) {
          if (!field.nullable) return false;
          continue;
        }
        if (!validate(field.schema, v)) return false;
      }
      // Closed objects: reject undeclared keys.
      for (const key of Object.keys(value)) {
        if (!(key in schema.fields)) return false;
      }
      return true;
    }
  }
}
