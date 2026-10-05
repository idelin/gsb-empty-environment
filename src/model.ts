export type Scalar = number | string;

export interface IntFieldDef {
  name: string;
  kind: 'int';
  min: number;
  max: number;
}

export interface EnumFieldDef {
  name: string;
  kind: 'enum';
  values: Scalar[];
}

export type FieldDef = IntFieldDef | EnumFieldDef;

export type Condition =
  | { op: 'any' }
  | { op: 'eq'; value: Scalar }
  | { op: 'in'; values: Scalar[] }
  | { op: 'range'; min?: number; max?: number }
  | { op: 'not'; cond: Condition }
  | { op: 'and'; conds: Condition[] }
  | { op: 'or'; conds: Condition[] };

export interface Rule {
  id: string;
  priority?: number;
  action?: string;
  conditions?: Record<string, Condition>;
}

export interface DecisionTable {
  name?: string;
  fields: FieldDef[];
  rules: Rule[];
}

export const MAX_DOMAIN_SIZE = 1_000_000;

function fail(msg: string): never {
  throw new Error(`invalid decision table: ${msg}`);
}

function isScalar(v: unknown): v is Scalar {
  return typeof v === 'number' || typeof v === 'string';
}

function validateCondition(raw: unknown, path: string): Condition {
  if (typeof raw !== 'object' || raw === null) fail(`${path}: condition must be an object`);
  const c = raw as Record<string, unknown>;
  switch (c.op) {
    case 'any':
      return { op: 'any' };
    case 'eq':
      if (!isScalar(c.value)) fail(`${path}: "eq" requires a scalar "value"`);
      return { op: 'eq', value: c.value };
    case 'in':
      if (!Array.isArray(c.values) || c.values.length === 0 || !c.values.every(isScalar)) {
        fail(`${path}: "in" requires a non-empty scalar array "values"`);
      }
      return { op: 'in', values: c.values as Scalar[] };
    case 'range': {
      const out: { op: 'range'; min?: number; max?: number } = { op: 'range' };
      if (c.min !== undefined) {
        if (typeof c.min !== 'number') fail(`${path}: "range.min" must be a number`);
        out.min = c.min;
      }
      if (c.max !== undefined) {
        if (typeof c.max !== 'number') fail(`${path}: "range.max" must be a number`);
        out.max = c.max;
      }
      return out;
    }
    case 'not':
      return { op: 'not', cond: validateCondition(c.cond, `${path}.not`) };
    case 'and':
    case 'or': {
      if (!Array.isArray(c.conds) || c.conds.length === 0) {
        fail(`${path}: "${c.op}" requires a non-empty array "conds"`);
      }
      return { op: c.op, conds: c.conds.map((x, i) => validateCondition(x, `${path}.${c.op}[${i}]`)) };
    }
    default:
      fail(`${path}: unknown op ${JSON.stringify(c.op)}`);
  }
}

export function validateTable(raw: unknown): DecisionTable {
  if (typeof raw !== 'object' || raw === null) fail('top level must be an object');
  const t = raw as Record<string, unknown>;

  if (!Array.isArray(t.fields) || t.fields.length === 0) fail('"fields" must be a non-empty array');
  const seenFields = new Set<string>();
  const fields: FieldDef[] = t.fields.map((f, i) => {
    const path = `fields[${i}]`;
    if (typeof f !== 'object' || f === null) fail(`${path}: must be an object`);
    const fd = f as Record<string, unknown>;
    if (typeof fd.name !== 'string' || fd.name === '') fail(`${path}: "name" must be a non-empty string`);
    if (seenFields.has(fd.name)) fail(`${path}: duplicate field name "${fd.name}"`);
    seenFields.add(fd.name);
    if (fd.kind === 'int') {
      if (typeof fd.min !== 'number' || typeof fd.max !== 'number' ||
          !Number.isInteger(fd.min) || !Number.isInteger(fd.max)) {
        fail(`${path}: int field requires integer "min" and "max"`);
      }
      if (fd.min > fd.max) fail(`${path}: min (${fd.min}) > max (${fd.max})`);
      if (fd.max - fd.min + 1 > MAX_DOMAIN_SIZE) {
        fail(`${path}: domain size ${fd.max - fd.min + 1} exceeds limit ${MAX_DOMAIN_SIZE}`);
      }
      return { name: fd.name, kind: 'int', min: fd.min, max: fd.max };
    }
    if (fd.kind === 'enum') {
      if (!Array.isArray(fd.values) || fd.values.length === 0 || !fd.values.every(isScalar)) {
        fail(`${path}: enum field requires a non-empty scalar array "values"`);
      }
      const values = fd.values as Scalar[];
      if (new Set(values).size !== values.length) fail(`${path}: enum values must be unique`);
      return { name: fd.name, kind: 'enum', values: [...values] };
    }
    fail(`${path}: "kind" must be "int" or "enum"`);
  });

  if (!Array.isArray(t.rules) || t.rules.length === 0) fail('"rules" must be a non-empty array');
  const seenRules = new Set<string>();
  const rules: Rule[] = t.rules.map((r, i) => {
    const path = `rules[${i}]`;
    if (typeof r !== 'object' || r === null) fail(`${path}: must be an object`);
    const rd = r as Record<string, unknown>;
    if (typeof rd.id !== 'string' || rd.id === '') fail(`${path}: "id" must be a non-empty string`);
    if (seenRules.has(rd.id)) fail(`${path}: duplicate rule id "${rd.id}"`);
    seenRules.add(rd.id);
    if (rd.priority !== undefined && typeof rd.priority !== 'number') {
      fail(`${path}: "priority" must be a number`);
    }
    if (rd.action !== undefined && typeof rd.action !== 'string') {
      fail(`${path}: "action" must be a string`);
    }
    const conditions: Record<string, Condition> = {};
    if (rd.conditions !== undefined) {
      if (typeof rd.conditions !== 'object' || rd.conditions === null) {
        fail(`${path}: "conditions" must be an object`);
      }
      for (const [fname, cond] of Object.entries(rd.conditions as Record<string, unknown>)) {
        if (!seenFields.has(fname)) fail(`${path}.conditions: unknown field "${fname}"`);
        conditions[fname] = validateCondition(cond, `${path}.conditions.${fname}`);
      }
    }
    return {
      id: rd.id,
      priority: rd.priority as number | undefined,
      action: rd.action as string | undefined,
      conditions,
    };
  });

  return {
    name: typeof t.name === 'string' ? t.name : undefined,
    fields,
    rules,
  };
}
