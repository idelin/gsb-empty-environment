/**
 * Policy conditions over explicit, read-only attributes.
 *
 * Deliberately tiny expression language: finite-set membership, integer
 * comparisons and boolean combinators. No functions, no I/O, no external
 * identity services - evaluation is a pure function of the attribute map.
 */

export type AttrValue = number | string | boolean;
export type Attrs = Readonly<Record<string, AttrValue>>;

export type Expr =
  | { op: 'has'; attr: string }
  | { op: 'eq'; attr: string; value: AttrValue }
  | { op: 'in'; attr: string; values: readonly (string | number)[] }
  | { op: 'intCmp'; attr: string; cmp: '<' | '<=' | '==' | '>=' | '>'; value: number }
  | { op: 'and'; args: readonly Expr[] }
  | { op: 'or'; args: readonly Expr[] }
  | { op: 'not'; arg: Expr };

export class UnknownAttributeError extends Error {
  constructor(attr: string) {
    super(`attribute "${attr}" is not present in the explicit attribute set`);
    this.name = 'UnknownAttributeError';
  }
}

export class NonIntegerAttributeError extends Error {
  constructor(attr: string, value: AttrValue) {
    super(`attribute "${attr}" must be an integer for intCmp, got ${JSON.stringify(value)}`);
    this.name = 'NonIntegerAttributeError';
  }
}

function readAttr(attrs: Attrs, attr: string): AttrValue {
  if (!Object.prototype.hasOwnProperty.call(attrs, attr)) {
    throw new UnknownAttributeError(attr);
  }
  return attrs[attr];
}

/** Pure evaluation; never mutates `attrs` (callers may pass frozen objects). */
export function evalExpr(expr: Expr, attrs: Attrs): boolean {
  switch (expr.op) {
    case 'has':
      return Object.prototype.hasOwnProperty.call(attrs, expr.attr);
    case 'eq':
      return readAttr(attrs, expr.attr) === expr.value;
    case 'in': {
      const v = readAttr(attrs, expr.attr);
      return expr.values.some((cand) => cand === v);
    }
    case 'intCmp': {
      const v = readAttr(attrs, expr.attr);
      if (typeof v !== 'number' || !Number.isInteger(v)) {
        throw new NonIntegerAttributeError(expr.attr, v);
      }
      if (!Number.isInteger(expr.value)) {
        throw new NonIntegerAttributeError(expr.attr, expr.value);
      }
      switch (expr.cmp) {
        case '<': return v < expr.value;
        case '<=': return v <= expr.value;
        case '==': return v === expr.value;
        case '>=': return v >= expr.value;
        case '>': return v > expr.value;
      }
      break;
    }
    case 'and':
      return expr.args.every((a) => evalExpr(a, attrs));
    case 'or':
      return expr.args.some((a) => evalExpr(a, attrs));
    case 'not':
      return !evalExpr(expr.arg, attrs);
  }
}
