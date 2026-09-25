/**
 * Schema with dependent fields and cross-field invariants.
 *
 * Validity is checked locally (no oracle budget spent): the reducer always
 * keeps the input schema-valid first, and only then cares about preserving
 * the judgment difference.
 */
import { evalCondition, type Condition, type Input } from './condition.ts';

export type FieldType =
  | { kind: 'int'; min: number; max: number }
  | { kind: 'set'; of: readonly string[]; minSize?: number; maxSize?: number }
  | { kind: 'bool' }
  | { kind: 'enum'; values: readonly string[] };

export interface FieldDef {
  type: FieldType;
  /** Always required, never removable. */
  required?: boolean;
  /**
   * Dependency: the field must be present iff `presentWhen` holds over the
   * rest of the input. While the condition holds the field is not removable.
   */
  presentWhen?: Condition;
}

export interface Schema {
  name: string;
  fields: Record<string, FieldDef>;
  /** Extra cross-field constraints that must hold for every valid input. */
  invariants?: Condition[];
}

export interface ValidationIssue {
  path: string;
  message: string;
}

export function validate(schema: Schema, input: Input): ValidationIssue[] {
  const issues: ValidationIssue[] = [];
  for (const [name, def] of Object.entries(schema.fields)) {
    const present = Object.prototype.hasOwnProperty.call(input, name);
    if (def.presentWhen) {
      const mustBePresent = evalCondition(def.presentWhen, without(input, name));
      if (mustBePresent && !present) {
        issues.push({ path: name, message: 'required by dependency presentWhen' });
      } else if (!mustBePresent && present) {
        issues.push({ path: name, message: 'forbidden by dependency presentWhen' });
      }
    } else if (def.required && !present) {
      issues.push({ path: name, message: 'required field missing' });
    }
    if (!present) continue;
    issues.push(...validateValue(name, def.type, input[name]));
  }
  for (const key of Object.keys(input)) {
    if (!(key in schema.fields)) {
      issues.push({ path: key, message: 'unknown attribute' });
    }
  }
  for (const inv of schema.invariants ?? []) {
    if (!evalCondition(inv, input)) {
      issues.push({ path: '<invariant>', message: `invariant violated: ${JSON.stringify(inv)}` });
    }
  }
  return issues;
}

function without(input: Input, key: string): Input {
  const copy = { ...input };
  delete copy[key];
  return copy;
}

function validateValue(path: string, type: FieldType, value: unknown): ValidationIssue[] {
  switch (type.kind) {
    case 'int': {
      if (typeof value !== 'number' || !Number.isInteger(value)) {
        return [{ path, message: 'expected integer' }];
      }
      if (value < type.min || value > type.max) {
        return [{ path, message: `out of range [${type.min}, ${type.max}]` }];
      }
      return [];
    }
    case 'set': {
      if (!Array.isArray(value) || value.some((v) => typeof v !== 'string')) {
        return [{ path, message: 'expected string array' }];
      }
      const uniq = new Set(value);
      if (uniq.size !== value.length) return [{ path, message: 'duplicate elements' }];
      for (const v of value) {
        if (!type.of.includes(v)) return [{ path, message: `element ${v} not in finite set` }];
      }
      if (type.minSize !== undefined && value.length < type.minSize) {
        return [{ path, message: `set smaller than minSize ${type.minSize}` }];
      }
      if (type.maxSize !== undefined && value.length > type.maxSize) {
        return [{ path, message: `set larger than maxSize ${type.maxSize}` }];
      }
      return [];
    }
    case 'bool':
      return typeof value === 'boolean' ? [] : [{ path, message: 'expected boolean' }];
    case 'enum':
      return typeof value === 'string' && type.values.includes(value)
        ? []
        : [{ path, message: `expected one of ${type.values.join('|')}` }];
  }
}

export function isValid(schema: Schema, input: Input): boolean {
  return validate(schema, input).length === 0;
}

/** True when the field may be absent from `input` without breaking validity. */
export function isRemovable(schema: Schema, name: string, input: Input): boolean {
  const def = schema.fields[name];
  if (!def || def.required) return false;
  if (!Object.prototype.hasOwnProperty.call(input, name)) return false;
  if (def.presentWhen && evalCondition(def.presentWhen, without(input, name))) return false;
  return true;
}
