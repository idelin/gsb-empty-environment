import { condToIntervals, type Expr, type Tri } from "./ast.ts";
import { contains } from "./interval.ts";

/** Attribute environment; an absent or undefined attribute is "missing". */
export type Env = Readonly<Record<string, number | undefined>>;

/**
 * Kleene three-valued evaluation.
 * A condition on a missing attribute is "unknown"; nothing is coerced.
 */
export function evalExpr(e: Expr, env: Env): Tri {
  switch (e.kind) {
    case "const":
      return e.value ? "true" : "false";
    case "not": {
      const r = evalExpr(e.child, env);
      if (r === "unknown") return "unknown";
      return r === "true" ? "false" : "true";
    }
    case "and": {
      let sawUnknown = false;
      for (const arg of e.args) {
        const r = evalExpr(arg, env);
        if (r === "false") return "false"; // false dominates
        if (r === "unknown") sawUnknown = true;
      }
      return sawUnknown ? "unknown" : "true";
    }
    case "or": {
      let sawUnknown = false;
      for (const arg of e.args) {
        const r = evalExpr(arg, env);
        if (r === "true") return "true"; // true dominates
        if (r === "unknown") sawUnknown = true;
      }
      return sawUnknown ? "unknown" : "false";
    }
    case "cond": {
      const v = env[e.attr];
      if (v === undefined) return "unknown";
      return contains(condToIntervals(e.cond), v) ? "true" : "false";
    }
  }
}
