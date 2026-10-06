import { formatExpr, type Expr } from "./ast.ts";
import { parseFormula } from "./parser.ts";
import {
  DIMENSIONLESS,
  dimDiv,
  dimEqual,
  dimMul,
  dimPow,
  formatDim,
  unitDim,
  type Dim,
} from "./units.ts";

export type Env = Readonly<Record<string, Dim>>;

export type Rule = "Num" | "Var" | "Add" | "Mul" | "Div" | "Pow";

// One node of the derivation tree: the judgment `expr : dim` plus the
// rule that derived it. `dim` is null exactly when `error` is set.
export interface Judgment {
  expr: Expr;
  rule: Rule;
  dim: Dim | null;
  error: string | null;
  children: Judgment[];
}

function ok(expr: Expr, rule: Rule, dim: Dim, children: Judgment[]): Judgment {
  return { expr, rule, dim, error: null, children };
}

function fail(expr: Expr, rule: Rule, error: string, children: Judgment[]): Judgment {
  return { expr, rule, dim: null, error, children };
}

export function check(expr: Expr, env: Env): Judgment {
  switch (expr.kind) {
    case "num":
      // Numeric constants are dimensionless. In particular, zero is NOT
      // polymorphic: it never implicitly takes on another unit.
      return ok(expr, "Num", DIMENSIONLESS, []);
    case "var": {
      const dim = env[expr.name];
      if (dim === undefined) {
        return fail(expr, "Var", `unknown variable '${expr.name}'`, []);
      }
      return ok(expr, "Var", dim, []);
    }
    case "add": {
      const left = check(expr.left, env);
      const right = check(expr.right, env);
      const children = [left, right];
      if (left.error || right.error) {
        return fail(expr, "Add", "operand is ill-typed", children);
      }
      if (!dimEqual(left.dim!, right.dim!)) {
        let message =
          `unit mismatch in addition: ` +
          `${formatDim(left.dim!)} vs ${formatDim(right.dim!)}`;
        if (expr.left.kind === "num" || expr.right.kind === "num") {
          message +=
            " (numeric constants are dimensionless and are never implicitly" +
            " converted to another unit — not even zero)";
        }
        return fail(expr, "Add", message, children);
      }
      return ok(expr, "Add", left.dim!, children);
    }
    case "mul":
    case "div": {
      const rule = expr.kind === "mul" ? "Mul" : "Div";
      const left = check(expr.left, env);
      const right = check(expr.right, env);
      const children = [left, right];
      if (left.error || right.error) {
        return fail(expr, rule, "operand is ill-typed", children);
      }
      const dim =
        expr.kind === "mul"
          ? dimMul(left.dim!, right.dim!)
          : dimDiv(left.dim!, right.dim!);
      return ok(expr, rule, dim, children);
    }
    case "pow": {
      const base = check(expr.base, env);
      if (base.error) {
        return fail(expr, "Pow", "base is ill-typed", [base]);
      }
      return ok(expr, "Pow", dimPow(base.dim!, expr.exponent), [base]);
    }
  }
}

export function checkFormula(src: string, env: Env): Judgment {
  return check(parseFormula(src), env);
}

// The minimal failing subexpression is the deepest (leftmost) judgment
// whose error is local: none of its children carry an error.
export function minimalFailure(judgment: Judgment): Judgment | null {
  for (const child of judgment.children) {
    const found = minimalFailure(child);
    if (found) return found;
  }
  return judgment.error ? judgment : null;
}

function hasError(judgment: Judgment): boolean {
  return judgment.error !== null || judgment.children.some(hasError);
}

// Renders the derivation tree, root first. The error message is shown
// only at the node where it originates, not at propagated ancestors.
export function renderJudgment(judgment: Judgment, indent = ""): string {
  const dim = judgment.dim ? formatDim(judgment.dim) : "✗";
  const local = judgment.error && !judgment.children.some(hasError);
  const note = local ? `  -- ${judgment.error}` : "";
  const lines = [
    `${indent}[${judgment.rule}] ${formatExpr(judgment.expr)} : ${dim}${note}`,
  ];
  for (const child of judgment.children) {
    lines.push(renderJudgment(child, indent + "  "));
  }
  return lines.join("\n");
}

// Parses a unit expression such as "kg*m/s^2" into a dimension vector,
// treating each declared base unit as its own basis vector.
export function dimFromUnitExpr(src: string, bases: readonly string[]): Dim {
  const env: Record<string, Dim> = {};
  for (const base of bases) env[base] = unitDim(base);
  const judgment = check(parseFormula(src), env);
  const failure = minimalFailure(judgment);
  if (failure || judgment.dim === null) {
    throw new Error(
      `invalid unit expression '${src}': ${failure?.error ?? judgment.error}`,
    );
  }
  return judgment.dim;
}
