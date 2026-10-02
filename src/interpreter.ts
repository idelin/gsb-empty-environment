import type { Schema } from './schema.ts';
import type { ValidationError, Verdict } from './validator.ts';

export interface InterpretOptions {
  /** Step budget. Exhaustion yields 'incomplete' — never 'invalid'. */
  maxSteps?: number;
}

export interface InterpretResult {
  verdict: Verdict;
  errors: ValidationError[];
  steps: number;
}

export const DEFAULT_MAX_STEPS = 10000;

class BudgetExhausted extends Error {}

/**
 * An independent interpreter that works directly on the raw schema document
 * (no compiled graph, no shared code with the graph validator). It exists to
 * cross-check the compiled validator on small bounded instances.
 */
export function interpret(rootSchema: Schema, value: unknown, opts: InterpretOptions = {}): InterpretResult {
  const maxSteps = opts.maxSteps ?? DEFAULT_MAX_STEPS;
  let steps = 0;
  const errors: ValidationError[] = [];

  function resolveRef(ref: string): { schemaPath: string; schema: unknown } {
    if (ref !== '#' && !ref.startsWith('#/')) throw new Error(`unsupported non-local ref "${ref}"`);
    const pointer = ref.slice(1);
    let cur: unknown = rootSchema;
    if (pointer !== '') {
      for (const raw of pointer.slice(1).split('/')) {
        const seg = raw.replace(/~1/g, '/').replace(/~0/g, '~');
        if (cur === null || typeof cur !== 'object' || !(seg in (cur as Record<string, unknown>))) {
          throw new Error(`dangling ref "${ref}"`);
        }
        cur = (cur as Record<string, unknown>)[seg];
      }
    }
    return { schemaPath: `#${pointer}`, schema: cur };
  }

  function evalSchema(
    schema: unknown,
    schemaPath: string,
    val: unknown,
    instPath: string,
    stack: Set<string>,
    out: ValidationError[],
  ): void {
    if (++steps > maxSteps) throw new BudgetExhausted();
    // Independent visit-state guard keyed by (schema location, instance location).
    const guard = `${schemaPath}@${instPath}`;
    if (stack.has(guard)) return;
    stack.add(guard);
    try {
      if (schema === null || typeof schema !== 'object' || Array.isArray(schema)) {
        throw new Error(`bad schema at ${schemaPath}`);
      }
      const s = schema as Record<string, unknown>;

      if (typeof s.$ref === 'string') {
        const target = resolveRef(s.$ref);
        evalSchema(target.schema, target.schemaPath, val, instPath, stack, out);
        return;
      }

      if (Array.isArray(s.oneOf)) {
        let matches = 0;
        for (let i = 0; i < s.oneOf.length; i++) {
          const branchOut: ValidationError[] = [];
          const branchStack = new Set(stack);
          evalSchema(s.oneOf[i], `${schemaPath}/oneOf/${i}`, val, instPath, branchStack, branchOut);
          if (branchOut.length === 0) matches++;
        }
        if (matches !== 1) {
          out.push({
            instancePath: instPath,
            schemaPath,
            message:
              matches === 0
                ? `no oneOf branch matched (${s.oneOf.length} branches tried)`
                : `matched ${matches} oneOf branches, expected exactly 1`,
          });
        }
        return;
      }

      switch (s.type) {
        case 'object': {
          if (typeof val !== 'object' || val === null || Array.isArray(val)) {
            out.push({ instancePath: instPath, schemaPath, message: `expected object, got ${describe(val)}` });
            return;
          }
          const rec = val as Record<string, unknown>;
          const required = Array.isArray(s.required) ? (s.required as string[]) : [];
          for (const req of required) {
            if (!(req in rec)) {
              out.push({ instancePath: instPath, schemaPath, message: `missing required property "${req}"` });
            }
          }
          const props = (s.properties ?? {}) as Record<string, unknown>;
          for (const key of Object.keys(props)) {
            if (key in rec) {
              evalSchema(
                props[key],
                `${schemaPath}/properties/${key.replace(/~/g, '~0').replace(/\//g, '~1')}`,
                rec[key],
                `${instPath}/${key.replace(/~/g, '~0').replace(/\//g, '~1')}`,
                stack,
                out,
              );
            }
          }
          return;
        }
        case 'array': {
          if (!Array.isArray(val)) {
            out.push({ instancePath: instPath, schemaPath, message: `expected array, got ${describe(val)}` });
            return;
          }
          for (let i = 0; i < val.length; i++) {
            evalSchema(s.items, `${schemaPath}/items`, val[i], `${instPath}/${i}`, stack, out);
          }
          return;
        }
        case 'string':
        case 'number':
        case 'boolean': {
          if (typeof val !== s.type) {
            out.push({ instancePath: instPath, schemaPath, message: `expected ${s.type}, got ${describe(val)}` });
          }
          return;
        }
        case 'null': {
          if (val !== null) {
            out.push({ instancePath: instPath, schemaPath, message: `expected null, got ${describe(val)}` });
          }
          return;
        }
        default:
          throw new Error(`unsupported schema construct at ${schemaPath}`);
      }
    } finally {
      stack.delete(guard);
    }
  }

  function describe(v: unknown): string {
    if (v === null) return 'null';
    if (Array.isArray(v)) return 'array';
    return typeof v;
  }

  try {
    evalSchema(rootSchema, '#', value, '', new Set(), errors);
  } catch (err) {
    if (err instanceof BudgetExhausted) {
      // Budget exhaustion is "incomplete" — it must never count as a schema failure.
      return { verdict: 'incomplete', errors: [], steps };
    }
    throw err;
  }

  // Dedup by (instancePath, schemaPath, message): same instance location hit
  // by different schema rules stays distinct; exact duplicates collapse.
  const seen = new Set<string>();
  const deduped: ValidationError[] = [];
  for (const e of errors) {
    const key = [e.instancePath, e.schemaPath, e.message].join(String.fromCharCode(0));
    if (seen.has(key)) continue;
    seen.add(key);
    deduped.push(e);
  }
  const verdict: Verdict = deduped.length > 0 ? 'invalid' : 'valid';
  return { verdict, errors: deduped, steps };
}
