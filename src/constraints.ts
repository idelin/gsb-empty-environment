import type { AttrSpec, Cond, Input, Schema, Value } from './model.ts';

const CMP: Record<string, (a: number, b: number) => boolean> = {
  '==': (a, b) => a === b,
  '!=': (a, b) => a !== b,
  '<': (a, b) => a < b,
  '<=': (a, b) => a <= b,
  '>': (a, b) => a > b,
  '>=': (a, b) => a >= b,
};

/**
 * Evaluate a condition against an input. Conditions over absent attributes
 * evaluate to false, so a policy can never be satisfied by implicit data.
 */
export function evalCond(cond: Cond, input: Input): boolean {
  switch (cond.op) {
    case 'int': {
      const v = input[cond.attr];
      return typeof v === 'number' && CMP[cond.cmp](v, cond.value);
    }
    case 'eq': {
      const v = input[cond.attr];
      return v !== undefined && v === cond.value;
    }
    case 'contains': {
      const v = input[cond.attr];
      return Array.isArray(v) && v.includes(cond.value);
    }
    case 'present':
      return input[cond.attr] !== undefined;
    case 'and':
      return cond.args.every((c) => evalCond(c, input));
    case 'or':
      return cond.args.some((c) => evalCond(c, input));
    case 'not':
      return !evalCond(cond.arg, input);
  }
}

export interface Validation {
  ok: boolean;
  errors: string[];
}

function checkAttr(name: string, spec: AttrSpec, value: Value, errors: string[]): void {
  switch (spec.kind) {
    case 'int':
      if (typeof value !== 'number' || !Number.isInteger(value)) {
        errors.push(`${name}: expected integer`);
      } else if (value < spec.min || value > spec.max) {
        errors.push(`${name}: ${value} outside [${spec.min}, ${spec.max}]`);
      }
      break;
    case 'enum':
      if (typeof value !== 'string' || !spec.values.includes(value)) {
        errors.push(`${name}: ${JSON.stringify(value)} not in enum`);
      }
      break;
    case 'set': {
      if (!Array.isArray(value)) {
        errors.push(`${name}: expected set (array)`);
        break;
      }
      const seen = new Set<string>();
      for (const el of value) {
        if (!spec.members.includes(el)) errors.push(`${name}: unknown member ${JSON.stringify(el)}`);
        if (seen.has(el)) errors.push(`${name}: duplicate member ${JSON.stringify(el)}`);
        seen.add(el);
      }
      if (spec.minSize !== undefined && value.length < spec.minSize) {
        errors.push(`${name}: set size ${value.length} < minSize ${spec.minSize}`);
      }
      break;
    }
    case 'bool':
      if (typeof value !== 'boolean') errors.push(`${name}: expected boolean`);
      break;
  }
}

/** Validate an input against the schema: declared attrs only, kind checks,
 *  ranges, finite-set membership, field dependencies and global constraints. */
export function validate(schema: Schema, input: Input): Validation {
  const errors: string[] = [];
  for (const [name, spec] of Object.entries(schema.attrs)) {
    if (spec.required && input[name] === undefined) errors.push(`${name}: required but absent`);
  }
  for (const [name, value] of Object.entries(input)) {
    const spec = schema.attrs[name];
    if (!spec) {
      errors.push(`${name}: undeclared attribute`);
      continue;
    }
    checkAttr(name, spec, value, errors);
    if (spec.dependsOn) {
      const dep = spec.dependsOn;
      if (input[dep.attr] !== dep.equals) {
        errors.push(`${name}: requires ${dep.attr} == ${JSON.stringify(dep.equals)}`);
      }
    }
  }
  for (const [i, c] of (schema.constraints ?? []).entries()) {
    if (!evalCond(c, input)) errors.push(`constraint[${i}] violated`);
  }
  return { ok: errors.length === 0, errors };
}
