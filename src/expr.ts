import { isPlainObject, type Expr, type ExprArg, type JsonObject, type JsonValue } from "./schema.ts";

export class ExprEvalError extends Error {
  readonly refPath: string | null;

  constructor(message: string, refPath: string | null) {
    super(message);
    this.name = "ExprEvalError";
    this.refPath = refPath;
  }
}

function isExpr(v: unknown): v is Expr {
  return isPlainObject(v) && (typeof v.$get === "string" || typeof v.op === "string");
}

/** Collect the first path segments of every `$get` in the expression (sibling-field dependencies). */
export function exprDependencies(expr: Expr): Set<string> {
  const deps = new Set<string>();
  const walk = (node: ExprArg): void => {
    if (!isExpr(node)) return;
    if ("$get" in node) {
      const first = node.$get.split(".")[0];
      if (first) deps.add(first);
      return;
    }
    if (node.op === "literal") return;
    for (const arg of node.args) walk(arg);
  };
  walk(expr);
  return deps;
}

function resolvePath(obj: JsonObject, path: string): JsonValue {
  const segments = path.split(".");
  let current: JsonValue = obj;
  for (const seg of segments) {
    if (!isPlainObject(current) || !Object.hasOwn(current, seg)) {
      throw new ExprEvalError(`cannot resolve reference "${path}"`, path);
    }
    current = current[seg];
  }
  return current;
}

function asNumber(v: JsonValue, op: string): number {
  if (typeof v !== "number") {
    throw new ExprEvalError(`operator "${op}" expects number operands, got ${JSON.stringify(v)}`, null);
  }
  return v;
}

export function evalExpr(expr: Expr, scope: JsonObject): JsonValue {
  if ("$get" in expr) return resolvePath(scope, expr.$get);
  switch (expr.op) {
    case "literal":
      return expr.value;
    case "concat":
      return expr.args.map((a) => stringify(evalArg(a, scope))).join("");
    case "add":
      return expr.args.reduce<number>((acc, a) => acc + asNumber(evalArg(a, scope), "add"), 0);
    case "sub": {
      const nums = expr.args.map((a) => asNumber(evalArg(a, scope), "sub"));
      if (nums.length === 0) throw new ExprEvalError(`operator "sub" needs at least 1 argument`, null);
      return nums.slice(1).reduce((acc, n) => acc - n, nums[0]);
    }
    case "mul":
      return expr.args.reduce<number>((acc, a) => acc * asNumber(evalArg(a, scope), "mul"), 1);
    case "div": {
      const nums = expr.args.map((a) => asNumber(evalArg(a, scope), "div"));
      if (nums.length < 2) throw new ExprEvalError(`operator "div" needs at least 2 arguments`, null);
      return nums.slice(1).reduce((acc, n) => acc / n, nums[0]);
    }
    case "coalesce":
      for (const a of expr.args) {
        const v = evalArg(a, scope);
        if (v !== null && v !== undefined) return v;
      }
      return null;
    default:
      throw new ExprEvalError(`unknown operator "${(expr as { op: string }).op}"`, null);
  }
}

function evalArg(arg: ExprArg, scope: JsonObject): JsonValue {
  return isExpr(arg) ? evalExpr(arg, scope) : arg;
}

function stringify(v: JsonValue): string {
  if (v === null) return "null";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}
