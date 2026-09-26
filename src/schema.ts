/**
 * Schema contract (the ONLY promised subset — anything else is rejected):
 *
 *   { groups: GroupRule[] }
 *
 * GroupRule:
 *   { kind: 'exactlyOne',           fields: string[], when?: Condition, id?: string }
 *   { kind: 'atLeastOne',           fields: string[], when?: Condition, id?: string }
 *   { kind: 'atMostK', k: integer,  fields: string[], when?: Condition, id?: string }
 *
 * Condition (tri-state, see conditions.ts):
 *   { op: 'present' | 'absent', field }
 *   { op: 'equals' | 'notEquals', field, value }
 *   { op: 'and', all: Condition[] } | { op: 'or', any: Condition[] } | { op: 'not', cond }
 *
 * Fields are top-level keys of a plain-object instance. Presence means
 * "own property", independent of the value (false / null / 0 / '' count).
 */

export type Condition =
  | { op: 'present'; field: string }
  | { op: 'absent'; field: string }
  | { op: 'equals'; field: string; value: unknown }
  | { op: 'notEquals'; field: string; value: unknown }
  | { op: 'and'; all: Condition[] }
  | { op: 'or'; any: Condition[] }
  | { op: 'not'; cond: Condition };

export type GroupKind = 'exactlyOne' | 'atLeastOne' | 'atMostK';

export interface GroupRule {
  kind: GroupKind;
  fields: string[];
  k?: number;
  when?: Condition;
  id?: string;
}

export interface Schema {
  groups: GroupRule[];
}

/** Contract policy for conditions that cannot be decided (referenced field missing). */
export type UndecidedPolicy = 'inactive' | 'active' | 'error';

export class SchemaError extends Error {
  readonly schemaPath: string;
  constructor(message: string, schemaPath: string) {
    super(`${message} (at ${schemaPath === '' ? '/' : schemaPath})`);
    this.name = 'SchemaError';
    this.schemaPath = schemaPath;
  }
}

const KINDS: ReadonlySet<string> = new Set(['exactlyOne', 'atLeastOne', 'atMostK']);
const OPS: ReadonlySet<string> = new Set([
  'present',
  'absent',
  'equals',
  'notEquals',
  'and',
  'or',
  'not',
]);

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function assertCondition(cond: unknown, path: string): asserts cond is Condition {
  if (!isPlainObject(cond)) throw new SchemaError('condition must be an object', path);
  const op = cond.op;
  if (typeof op !== 'string' || !OPS.has(op)) {
    throw new SchemaError(`unsupported condition op ${JSON.stringify(op)}`, `${path}/op`);
  }
  switch (op) {
    case 'present':
    case 'absent':
      if (typeof cond.field !== 'string') {
        throw new SchemaError('condition.field must be a string', `${path}/field`);
      }
      return;
    case 'equals':
    case 'notEquals':
      if (typeof cond.field !== 'string') {
        throw new SchemaError('condition.field must be a string', `${path}/field`);
      }
      if (!('value' in cond)) {
        throw new SchemaError('condition.value is required', `${path}/value`);
      }
      return;
    case 'and':
    case 'or': {
      const key = op === 'and' ? 'all' : 'any';
      const list = cond[key];
      if (!Array.isArray(list) || list.length === 0) {
        throw new SchemaError(`condition.${key} must be a non-empty array`, `${path}/${key}`);
      }
      list.forEach((c, i) => assertCondition(c, `${path}/${key}/${i}`));
      return;
    }
    case 'not':
      assertCondition(cond.cond, `${path}/cond`);
      return;
  }
}

function assertGroup(rule: unknown, path: string): asserts rule is GroupRule {
  if (!isPlainObject(rule)) throw new SchemaError('group rule must be an object', path);
  if (typeof rule.kind !== 'string' || !KINDS.has(rule.kind)) {
    throw new SchemaError(`unsupported group kind ${JSON.stringify(rule.kind)}`, `${path}/kind`);
  }
  if (
    !Array.isArray(rule.fields) ||
    rule.fields.length === 0 ||
    !rule.fields.every((f) => typeof f === 'string')
  ) {
    throw new SchemaError('group.fields must be a non-empty array of strings', `${path}/fields`);
  }
  if (new Set(rule.fields).size !== rule.fields.length) {
    throw new SchemaError('group.fields must not contain duplicates', `${path}/fields`);
  }
  if (rule.kind === 'atMostK') {
    if (!Number.isInteger(rule.k) || (rule.k as number) < 0) {
      throw new SchemaError('group.k must be a non-negative integer for atMostK', `${path}/k`);
    }
  }
  if (rule.when !== undefined) assertCondition(rule.when, `${path}/when`);
}

/** Rejects anything outside the implemented schema subset. */
export function assertSchema(schema: unknown): asserts schema is Schema {
  if (!isPlainObject(schema)) throw new SchemaError('schema must be an object', '');
  if (!Array.isArray(schema.groups)) {
    throw new SchemaError('schema.groups must be an array', '/groups');
  }
  schema.groups.forEach((g, i) => assertGroup(g, `/groups/${i}`));
}

export function conditionFields(cond: Condition): string[] {
  switch (cond.op) {
    case 'present':
    case 'absent':
    case 'equals':
    case 'notEquals':
      return [cond.field];
    case 'and':
      return cond.all.flatMap(conditionFields);
    case 'or':
      return cond.any.flatMap(conditionFields);
    case 'not':
      return conditionFields(cond.cond);
  }
}

/** Sorted unique field names referenced by groups and conditions. */
export function collectFields(schema: Schema): string[] {
  const out = new Set<string>();
  for (const rule of schema.groups) {
    for (const f of rule.fields) out.add(f);
    if (rule.when) for (const f of conditionFields(rule.when)) out.add(f);
  }
  return [...out].sort();
}
