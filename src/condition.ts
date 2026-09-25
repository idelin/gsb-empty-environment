/**
 * Condition AST. Policies may only use:
 *  - integer comparisons against a declared attribute (eq/ne/lt/le/gt/ge)
 *  - membership in an explicit finite set of integers
 *  - boolean combinators (and/or/not) and constants
 * Everything is a pure function of explicitly declared attributes; there is
 * no escape hatch to external identity/enrichment services.
 */
export type CmpOp = 'eq' | 'ne' | 'lt' | 'le' | 'gt' | 'ge';

export type Condition =
  | { kind: 'cmp'; attr: string; op: CmpOp; value: number }
  | { kind: 'in'; attr: string; set: readonly number[] }
  | { kind: 'and'; args: readonly Condition[] }
  | { kind: 'or'; args: readonly Condition[] }
  | { kind: 'not'; arg: Condition }
  | { kind: 'const'; value: boolean };

export type AttrValues = Readonly<Record<string, number>>;

/** Evaluates a condition against a complete assignment of attribute values. */
export function evalCondition(cond: Condition, attrs: AttrValues): boolean {
  switch (cond.kind) {
    case 'const':
      return cond.value;
    case 'cmp': {
      const actual = requireAttr(attrs, cond.attr);
      switch (cond.op) {
        case 'eq': return actual === cond.value;
        case 'ne': return actual !== cond.value;
        case 'lt': return actual < cond.value;
        case 'le': return actual <= cond.value;
        case 'gt': return actual > cond.value;
        case 'ge': return actual >= cond.value;
      }
      break;
    }
    case 'in':
      return cond.set.includes(requireAttr(attrs, cond.attr));
    case 'and':
      return cond.args.every((c) => evalCondition(c, attrs));
    case 'or':
      return cond.args.some((c) => evalCondition(c, attrs));
    case 'not':
      return !evalCondition(cond.arg, attrs);
  }
}

function requireAttr(attrs: AttrValues, name: string): number {
  const v = attrs[name];
  if (v === undefined) {
    throw new Error(`attribute "${name}" has no value in this assignment`);
  }
  return v;
}

/** Names of attributes the condition reads. */
export function referencedAttrs(cond: Condition, into: Set<string> = new Set()): Set<string> {
  switch (cond.kind) {
    case 'const':
      return into;
    case 'cmp':
    case 'in':
      into.add(cond.attr);
      return into;
    case 'and':
    case 'or':
      for (const c of cond.args) referencedAttrs(c, into);
      return into;
    case 'not':
      return referencedAttrs(cond.arg, into);
  }
}

/** Structural validation against the declared attribute universe. */
export function validateCondition(cond: Condition, declaredAttrs: ReadonlySet<string>, path = 'condition'): void {
  switch (cond.kind) {
    case 'const':
      if (typeof cond.value !== 'boolean') throw new PolicyValidationError(`${path}: const value must be boolean`);
      return;
    case 'cmp':
      requireDeclared(cond.attr, declaredAttrs, path);
      if (!Number.isInteger(cond.value)) {
        throw new PolicyValidationError(`${path}: comparison value must be an integer, got ${cond.value}`);
      }
      return;
    case 'in':
      requireDeclared(cond.attr, declaredAttrs, path);
      if (!Array.isArray(cond.set) || cond.set.length === 0) {
        throw new PolicyValidationError(`${path}: "in" requires a non-empty finite set`);
      }
      for (const v of cond.set) {
        if (!Number.isInteger(v)) {
          throw new PolicyValidationError(`${path}: set members must be integers, got ${v}`);
        }
      }
      return;
    case 'and':
    case 'or':
      if (!Array.isArray(cond.args) || cond.args.length === 0) {
        throw new PolicyValidationError(`${path}: "${cond.kind}" requires at least one argument`);
      }
      cond.args.forEach((c, i) => validateCondition(c, declaredAttrs, `${path}.${cond.kind}[${i}]`));
      return;
    case 'not':
      validateCondition(cond.arg, declaredAttrs, `${path}.not`);
      return;
    default:
      throw new PolicyValidationError(`${path}: unknown condition node ${JSON.stringify(cond)}`);
  }
}

function requireDeclared(attr: string, declared: ReadonlySet<string>, path: string): void {
  if (!declared.has(attr)) {
    throw new PolicyValidationError(`${path}: attribute "${attr}" is not declared in the policy`);
  }
}

export class PolicyValidationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PolicyValidationError';
  }
}

/**
 * Canonical serialization of a condition, used to detect whether two rules
 * sharing an id carry identical or conflicting content.
 */
export function canonicalCondition(cond: Condition): string {
  switch (cond.kind) {
    case 'const':
      return `const(${cond.value})`;
    case 'cmp':
      return `cmp(${cond.attr},${cond.op},${cond.value})`;
    case 'in':
      return `in(${cond.attr},[${[...cond.set].sort((a, b) => a - b).join(',')}])`;
    case 'and':
    case 'or': {
      const parts = cond.args.map(canonicalCondition).sort();
      return `${cond.kind}(${parts.join(',')})`;
    }
    case 'not':
      return `not(${canonicalCondition(cond.arg)})`;
  }
}
