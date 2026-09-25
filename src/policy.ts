import { condToIntervals, type Expr } from "./ast.ts";
import { evalExpr, type Env, type Tri } from "./eval.ts";

/** Explicit attribute declaration: a finite integer domain plus nullability. */
export interface AttrDecl {
  readonly name: string;
  readonly min: number;
  readonly max: number;
  readonly required: boolean;
}

/**
 * A policy is a boolean expression over explicitly declared attributes.
 * Only finite sets, integer conditions and boolean operators exist here;
 * there is no hook for external identity/attribute services.
 */
export interface Policy {
  readonly name: string;
  readonly attributes: readonly AttrDecl[];
  readonly expr: Expr;
}

export function validatePolicy(policy: Policy): string[] {
  const errors: string[] = [];
  const names = new Set<string>();
  for (const a of policy.attributes) {
    if (names.has(a.name)) errors.push(`duplicate attribute: ${a.name}`);
    names.add(a.name);
    if (!Number.isInteger(a.min) || !Number.isInteger(a.max) || a.min > a.max) {
      errors.push(`attribute ${a.name}: invalid integer domain [${a.min}, ${a.max}]`);
    }
  }
  const walk = (e: Expr): void => {
    switch (e.kind) {
      case "cond": {
        if (!names.has(e.attr)) {
          errors.push(`expression references undeclared attribute: ${e.attr}`);
        }
        if (e.cond.type === "in") {
          for (const v of e.cond.values) {
            if (!Number.isInteger(v)) {
              errors.push(`condition on ${e.attr}: non-integer set value ${v}`);
            }
          }
        } else {
          for (const iv of condToIntervals(e.cond)) {
            if (
              (Number.isFinite(iv.lo) && !Number.isInteger(iv.lo)) ||
              (Number.isFinite(iv.hi) && !Number.isInteger(iv.hi))
            ) {
              errors.push(`condition on ${e.attr}: non-integer interval bound`);
            }
          }
        }
        break;
      }
      case "not":
        walk(e.child);
        break;
      case "and":
      case "or":
        e.args.forEach(walk);
        break;
      case "const":
        break;
    }
  };
  walk(policy.expr);
  return errors;
}

/**
 * Evaluate a policy against a context object.
 * Reads ONLY the explicitly declared attributes; any other key in the
 * context is never touched. A declared attribute that is absent or not an
 * integer is treated as missing (its conditions evaluate to "unknown").
 */
export function evaluatePolicy(
  policy: Policy,
  context: Readonly<Record<string, unknown>>,
): Tri {
  const errors = validatePolicy(policy);
  if (errors.length > 0) throw new Error(`invalid policy: ${errors.join("; ")}`);
  const env: Record<string, number | undefined> = {};
  for (const a of policy.attributes) {
    const v = context[a.name];
    env[a.name] = typeof v === "number" && Number.isInteger(v) ? v : undefined;
  }
  return evalExpr(policy.expr, env as Env);
}
