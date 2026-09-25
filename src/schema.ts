/**
 * 轻量 JSON Schema 校验器（每个迁移步骤的输入 / 输出都用它验证）。
 *
 * 支持：null / boolean / number / integer / string / array / object、
 * enum、const、required、optional、additionalProperties、
 * minItems/maxItems、min/max、items 与 properties 递归。
 */
import type { JsonValue } from './json.ts';
import type { ValidationIssue } from './errors.ts';

export type PrimitiveType =
  | 'null'
  | 'boolean'
  | 'number'
  | 'integer'
  | 'string'
  | 'array'
  | 'object';

export interface Schema {
  type?: PrimitiveType;
  enum?: JsonValue[];
  const?: JsonValue;
  required?: string[];
  optional?: string[];
  properties?: Record<string, Schema>;
  additionalProperties?: boolean;
  items?: Schema;
  minItems?: number;
  maxItems?: number;
  min?: number;
  max?: number;
  anyOf?: Schema[];
}

function typeName(v: JsonValue): PrimitiveType {
  if (v === null) return 'null';
  if (Array.isArray(v)) return 'array';
  const t = typeof v;
  if (t === 'number') return Number.isInteger(v) ? 'integer' : 'number';
  return t as PrimitiveType;
}

function typeMatches(value: JsonValue, type: PrimitiveType): boolean {
  const name = typeName(value);
  if (type === 'number') return name === 'number' || name === 'integer';
  return name === type;
}

function validate(
  value: JsonValue,
  schema: Schema,
  path: string,
  issues: ValidationIssue[],
): void {
  if (schema.type !== undefined && !typeMatches(value, schema.type)) {
    issues.push({
      path,
      reason: 'type mismatch',
      expected: schema.type,
      actual: typeName(value),
    });
    return;
  }

  if (schema.const !== undefined) {
    if (JSON.stringify(value) !== JSON.stringify(schema.const)) {
      issues.push({ path, reason: 'const mismatch', actual: typeName(value) });
    }
  }

  if (schema.enum !== undefined && !schema.enum.some((m) => JSON.stringify(m) === JSON.stringify(value))) {
    issues.push({ path, reason: 'value not in enum', actual: typeName(value) });
  }

  if (schema.anyOf !== undefined) {
    const subIssues: ValidationIssue[] = [];
    const matched = schema.anyOf.some((branch) => {
      const branchIssues: ValidationIssue[] = [];
      validate(value, branch, path, branchIssues);
      if (branchIssues.length === 0) return true;
      subIssues.push(...branchIssues);
      return false;
    });
    if (!matched) {
      issues.push({ path, reason: 'no anyOf branch matched' });
      issues.push(...subIssues);
    }
  }

  if (typeof value === 'number') {
    if (schema.min !== undefined && value < schema.min) {
      issues.push({ path, reason: 'below minimum', expected: String(schema.min), actual: String(value) });
    }
    if (schema.max !== undefined && value > schema.max) {
      issues.push({ path, reason: 'above maximum', expected: String(schema.max), actual: String(value) });
    }
  }

  if (Array.isArray(value)) {
    if (schema.minItems !== undefined && value.length < schema.minItems) {
      issues.push({ path, reason: 'array too short', expected: String(schema.minItems), actual: String(value.length) });
    }
    if (schema.maxItems !== undefined && value.length > schema.maxItems) {
      issues.push({ path, reason: 'array too long', expected: String(schema.maxItems), actual: String(value.length) });
    }
    if (schema.items) {
      value.forEach((item, i) => validate(item, schema.items as Schema, `${path}/${i}`, issues));
    }
  }

  if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
    const obj = value as Record<string, JsonValue>;
    const props = schema.properties ?? {};

    for (const req of schema.required ?? []) {
      if (!(req in obj)) {
        issues.push({ path, reason: 'missing required property', expected: req, actual: 'undefined' });
      }
    }

    for (const [key, child] of Object.entries(obj)) {
      if (key in props) {
        validate(child, props[key] as Schema, `${path}/${key.replace(/~/g, '~0').replace(/\//g, '~1')}`, issues);
      } else if (schema.additionalProperties === false) {
        const childPath = `${path}/${key.replace(/~/g, '~0').replace(/\//g, '~1')}`;
        issues.push({ path: childPath, reason: 'additional property not allowed', actual: key });
      }
    }
  }
}

/** 校验并返回问题列表（空数组 = 通过）。 */
export function validateSchema(value: JsonValue, schema: Schema): ValidationIssue[] {
  const issues: ValidationIssue[] = [];
  validate(value, schema, '', issues);
  return issues;
}
