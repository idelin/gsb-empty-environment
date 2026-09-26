import type { Group, Json, Schema, UndecidedPolicy } from './types.ts';
import {
  evalCondition,
  missingEqualsFields,
  type FieldGetter,
} from './condition.ts';
import { hasField } from './presence.ts';

export interface ValidationError {
  keyword: string;
  instancePath: string;
  rulePath: string;
  message: string;
  details?: Record<string, unknown>;
}

export interface ValidationResult {
  valid: boolean;
  errors: ValidationError[];
}

export class SchemaError extends Error {}

const GROUP_KINDS = new Set(['exactlyOne', 'atLeastOne', 'atMost']);
const COND_OPS = new Set(['present', 'absent', 'equals', 'and', 'or', 'not']);
const POLICIES = new Set(['disabled', 'enabled', 'error']);

export function assertSchema(schema: Schema): void {
  if (typeof schema !== 'object' || schema === null || Array.isArray(schema)) {
    throw new SchemaError('schema must be an object');
  }
  const policy = schema.onUndecidedCondition ?? 'disabled';
  if (!POLICIES.has(policy)) {
    throw new SchemaError(`unsupported onUndecidedCondition: ${String(schema.onUndecidedCondition)}`);
  }
  const groups = schema.groups ?? [];
  if (!Array.isArray(groups)) throw new SchemaError('schema.groups must be an array');
  groups.forEach((g, i) => {
    const where = `/groups/${i}`;
    if (!GROUP_KINDS.has(g.kind)) {
      throw new SchemaError(`${where}: unsupported kind ${String(g.kind)}`);
    }
    if (!Array.isArray(g.fields) || g.fields.length === 0 || g.fields.some((f) => typeof f !== 'string')) {
      throw new SchemaError(`${where}: fields must be a non-empty string array`);
    }
    if (g.kind === 'atMost') {
      if (typeof g.k !== 'number' || !Number.isInteger(g.k) || g.k < 0) {
        throw new SchemaError(`${where}: atMost requires integer k >= 0`);
      }
    } else if (g.k !== undefined) {
      throw new SchemaError(`${where}: k is only meaningful for atMost`);
    }
    if (g.when !== undefined) assertCondition(g.when, `${where}/when`);
  });
}

function assertCondition(cond: unknown, where: string): void {
  if (typeof cond !== 'object' || cond === null || !COND_OPS.has((cond as { op?: string }).op ?? '')) {
    throw new SchemaError(`${where}: unsupported condition`);
  }
  const c = cond as { op: string; field?: unknown; all?: unknown; any?: unknown; cond?: unknown };
  switch (c.op) {
    case 'present':
    case 'absent':
      if (typeof c.field !== 'string') throw new SchemaError(`${where}: field must be a string`);
      break;
    case 'equals':
      if (typeof c.field !== 'string') throw new SchemaError(`${where}: field must be a string`);
      if (!('value' in (c as object))) throw new SchemaError(`${where}: equals requires value`);
      break;
    case 'and':
      if (!Array.isArray(c.all) || c.all.length === 0) throw new SchemaError(`${where}: and requires non-empty all`);
      (c.all as unknown[]).forEach((x, i) => assertCondition(x, `${where}/all/${i}`));
      break;
    case 'or':
      if (!Array.isArray(c.any) || c.any.length === 0) throw new SchemaError(`${where}: or requires non-empty any`);
      (c.any as unknown[]).forEach((x, i) => assertCondition(x, `${where}/any/${i}`));
      break;
    case 'not':
      assertCondition(c.cond, `${where}/cond`);
      break;
  }
}

export function instanceGetter(instance: Record<string, Json>): FieldGetter {
  return (field) =>
    hasField(instance, field)
      ? { state: 'value', value: instance[field] }
      : { state: 'absent' };
}

function checkGroup(
  group: Group,
  index: number,
  instance: Record<string, Json>,
  policy: UndecidedPolicy,
): ValidationError[] {
  const rulePath = `/groups/${index}`;
  if (group.when !== undefined) {
    const get = instanceGetter(instance);
    const r = evalCondition(group.when, get);
    if (r === false) return [];
    if (r === 'undecided') {
      if (policy === 'disabled') return [];
      if (policy === 'error') {
        const missing = [...missingEqualsFields(group.when, get)].sort();
        return [
          {
            keyword: 'condition',
            instancePath: missing.length > 0 ? `/${missing[0]}` : '',
            rulePath: `${rulePath}/when`,
            message: `enabling condition is undecided: missing field(s) ${missing.join(', ')}`,
            details: { missingFields: missing, policy },
          },
        ];
      }
    }
  }
  const present = group.fields.filter((f) => hasField(instance, f));
  const n = present.length;
  const fail = (message: string, extra: Record<string, unknown>): ValidationError[] => [
    {
      keyword: group.kind,
      instancePath: '',
      rulePath,
      message,
      details: { fields: group.fields, present, ...extra },
    },
  ];
  switch (group.kind) {
    case 'exactlyOne':
      if (n !== 1) {
        return fail(`exactlyOne: expected exactly 1 of [${group.fields.join(', ')}], got ${n}`, {
          expected: 1,
          actual: n,
        });
      }
      return [];
    case 'atLeastOne':
      if (n < 1) {
        return fail(`atLeastOne: expected at least 1 of [${group.fields.join(', ')}], got 0`, {
          expected: '>=1',
          actual: 0,
        });
      }
      return [];
    case 'atMost':
      if (n > group.k!) {
        return fail(
          `atMost: expected at most ${group.k} of [${group.fields.join(', ')}], got ${n}`,
          { expected: `<=${group.k}`, actual: n },
        );
      }
      return [];
  }
}

export function validate(instance: Record<string, Json>, schema: Schema): ValidationResult {
  assertSchema(schema);
  if (typeof instance !== 'object' || instance === null || Array.isArray(instance)) {
    throw new TypeError('instance must be a plain object');
  }
  const policy = schema.onUndecidedCondition ?? 'disabled';
  const errors: ValidationError[] = [];
  (schema.groups ?? []).forEach((g, i) => {
    errors.push(...checkGroup(g, i, instance, policy));
  });
  return { valid: errors.length === 0, errors };
}
