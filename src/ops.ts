import type { Input, Schema } from './model.ts';

/**
 * Declared basic reduction operations. The 1-minimality certificate is
 * defined against exactly this set.
 */
export type Op =
  | { type: 'remove-attr'; attr: string }
  | { type: 'remove-set-element'; attr: string; element: string }
  | { type: 'decrement-int'; attr: string };

export function describeOp(op: Op): string {
  switch (op.type) {
    case 'remove-attr':
      return `remove attribute ${op.attr}`;
    case 'remove-set-element':
      return `remove ${JSON.stringify(op.element)} from ${op.attr}`;
    case 'decrement-int':
      return `decrement ${op.attr} by 1`;
  }
}

export function cloneInput(input: Input): Input {
  const out: Input = {};
  for (const [k, v] of Object.entries(input)) out[k] = Array.isArray(v) ? [...v] : v;
  return out;
}

/**
 * Remove an attribute together with every attribute whose dependency would
 * become unsatisfied (recursively), so the result can stay schema-valid.
 */
export function cascadeRemove(schema: Schema, input: Input, attr: string): Input {
  const out = cloneInput(input);
  const queue = [attr];
  while (queue.length > 0) {
    const target = queue.pop() as string;
    delete out[target];
    for (const [name, spec] of Object.entries(schema.attrs)) {
      if (out[name] === undefined || !spec.dependsOn) continue;
      const dep = spec.dependsOn;
      if (out[dep.attr] !== dep.equals) queue.push(name);
    }
  }
  return out;
}

/** Apply a basic op; returns null when the op is not applicable to this input. */
export function applyOp(schema: Schema, input: Input, op: Op): Input | null {
  switch (op.type) {
    case 'remove-attr': {
      const spec = schema.attrs[op.attr];
      if (!spec || spec.required || input[op.attr] === undefined) return null;
      return cascadeRemove(schema, input, op.attr);
    }
    case 'remove-set-element': {
      const spec = schema.attrs[op.attr];
      const v = input[op.attr];
      if (!spec || spec.kind !== 'set' || !Array.isArray(v) || !v.includes(op.element)) return null;
      const out = cloneInput(input);
      out[op.attr] = v.filter((el) => el !== op.element);
      return out;
    }
    case 'decrement-int': {
      const spec = schema.attrs[op.attr];
      const v = input[op.attr];
      if (!spec || spec.kind !== 'int' || typeof v !== 'number') return null;
      if (v - 1 < spec.min) return null;
      const out = cloneInput(input);
      out[op.attr] = v - 1;
      return out;
    }
  }
}

/** Enumerate every basic op applicable to the given input, in stable order. */
export function enumerateOps(schema: Schema, input: Input): Op[] {
  const ops: Op[] = [];
  for (const name of Object.keys(input).sort()) {
    const spec = schema.attrs[name];
    if (!spec) continue;
    if (!spec.required) ops.push({ type: 'remove-attr', attr: name });
    if (spec.kind === 'set') {
      const v = input[name];
      if (Array.isArray(v)) {
        const minSize = spec.minSize ?? 0;
        if (v.length > minSize) {
          for (const el of v) ops.push({ type: 'remove-set-element', attr: name, element: el });
        }
      }
    }
    if (spec.kind === 'int') {
      const v = input[name];
      if (typeof v === 'number' && v - 1 >= spec.min) ops.push({ type: 'decrement-int', attr: name });
    }
  }
  return ops;
}

/** Size measure: attribute count + set sizes + integer magnitudes above min. */
export function measure(schema: Schema, input: Input): number {
  let m = 0;
  for (const [name, v] of Object.entries(input)) {
    const spec = schema.attrs[name];
    m += 1;
    if (Array.isArray(v)) m += v.length;
    else if (typeof v === 'number' && spec?.kind === 'int') m += v - spec.min;
  }
  return m;
}
