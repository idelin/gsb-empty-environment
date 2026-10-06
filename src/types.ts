/**
 * Algebraic data type schemas.
 *
 * A type is a finite set of constructors; each constructor has a finite list
 * of fields whose types are referenced by name. Recursion is expressed by a
 * constructor field referencing its own (or a mutually recursive) type name,
 * so types are never unfolded eagerly and recursive types cannot diverge the
 * checker.
 */

export interface CtorDef {
  name: string;
  /** Type names of the constructor fields (positional). */
  fields: string[];
}

export interface TypeDef {
  name: string;
  ctors: CtorDef[];
}

export type TypeEnv = Record<string, TypeDef>;

/** A concrete value of an algebraic data type. */
export interface Value {
  ctor: string;
  args: Value[];
}

export function showValue(v: Value): string {
  return v.args.length === 0
    ? v.ctor
    : `${v.ctor}(${v.args.map(showValue).join(", ")})`;
}

export function valueDepth(v: Value): number {
  let d = 0;
  for (const a of v.args) d = Math.max(d, valueDepth(a));
  return 1 + d;
}

export function valueEq(a: Value, b: Value): boolean {
  if (a.ctor !== b.ctor || a.args.length !== b.args.length) return false;
  return a.args.every((x, i) => valueEq(x, b.args[i]!));
}
