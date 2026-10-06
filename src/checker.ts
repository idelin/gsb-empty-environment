import type { Expr } from "./ast.ts";
import { slice } from "./ast.ts";
import {
  DIMENSIONLESS,
  divUnits,
  formatUnit,
  mulUnits,
  powUnit,
  unitsEqual,
  type Unit,
} from "./units.ts";

export type Env = ReadonlyMap<string, Unit>;

export type CheckError =
  | { code: "UNIT_MISMATCH"; op: "+" | "-"; left: Unit; right: Unit }
  | { code: "UNDEFINED_VAR"; name: string }
  | { code: "PROPAGATED" };

export interface DerivationNode {
  expr: Expr;
  /** Inferred unit, or null when this subtree failed to check. */
  unit: Unit | null;
  /** The judgement/rule applied at this node, e.g. "x : m" or "add(m, m) => m". */
  rule: string;
  error: CheckError | null;
  children: DerivationNode[];
}

export interface CheckResult {
  tree: DerivationNode;
  /** Overall unit of the formula, or null if checking failed. */
  unit: Unit | null;
  /** Minimal erroring subexpressions: nodes that failed while all their
   *  children succeeded — the exact spots where checking breaks. */
  minimalErrors: DerivationNode[];
}

function checkRec(expr: Expr, env: Env): DerivationNode {
  switch (expr.kind) {
    case "num":
      // Every literal — including 0 — is dimensionless. Zero is NOT
      // polymorphic: it may not silently adopt a neighbouring unit.
      return {
        expr,
        unit: DIMENSIONLESS,
        rule: `${expr.value} : 1`,
        error: null,
        children: [],
      };
    case "var": {
      const unit = env.get(expr.name);
      if (unit === undefined) {
        return {
          expr,
          unit: null,
          rule: `${expr.name} : <undefined>`,
          error: { code: "UNDEFINED_VAR", name: expr.name },
          children: [],
        };
      }
      return {
        expr,
        unit,
        rule: `${expr.name} : ${formatUnit(unit)}`,
        error: null,
        children: [],
      };
    }
    case "add": {
      const left = checkRec(expr.left, env);
      const right = checkRec(expr.right, env);
      const children = [left, right];
      if (left.unit === null || right.unit === null) {
        return {
          expr,
          unit: null,
          rule: `${expr.op}(?) : <error>`,
          error: { code: "PROPAGATED" },
          children,
        };
      }
      if (!unitsEqual(left.unit, right.unit)) {
        return {
          expr,
          unit: null,
          rule: `${expr.op}(${formatUnit(left.unit)}, ${formatUnit(right.unit)}) : <mismatch>`,
          error: { code: "UNIT_MISMATCH", op: expr.op, left: left.unit, right: right.unit },
          children,
        };
      }
      return {
        expr,
        unit: left.unit,
        rule: `${expr.op}(${formatUnit(left.unit)}, ${formatUnit(right.unit)}) => ${formatUnit(left.unit)}`,
        error: null,
        children,
      };
    }
    case "mul": {
      const left = checkRec(expr.left, env);
      const right = checkRec(expr.right, env);
      const children = [left, right];
      if (left.unit === null || right.unit === null) {
        return {
          expr,
          unit: null,
          rule: `${expr.op}(?) : <error>`,
          error: { code: "PROPAGATED" },
          children,
        };
      }
      const unit = expr.op === "*" ? mulUnits(left.unit, right.unit) : divUnits(left.unit, right.unit);
      return {
        expr,
        unit,
        rule: `${expr.op}(${formatUnit(left.unit)}, ${formatUnit(right.unit)}) => ${formatUnit(unit)}`,
        error: null,
        children,
      };
    }
    case "pow": {
      const base = checkRec(expr.base, env);
      if (base.unit === null) {
        return {
          expr,
          unit: null,
          rule: `^(?) : <error>`,
          error: { code: "PROPAGATED" },
          children: [base],
        };
      }
      const unit = powUnit(base.unit, expr.exponent);
      return {
        expr,
        unit,
        rule: `(${formatUnit(base.unit)})^${expr.exponent} => ${formatUnit(unit)}`,
        error: null,
        children: [base],
      };
    }
  }
}

function collectMinimal(node: DerivationNode, out: DerivationNode[]): void {
  if (node.error === null) return;
  const childHasError = node.children.some((c) => c.error !== null);
  if (!childHasError) {
    out.push(node);
    return;
  }
  for (const c of node.children) collectMinimal(c, out);
}

export function check(expr: Expr, env: Env): CheckResult {
  const tree = checkRec(expr, env);
  const minimalErrors: DerivationNode[] = [];
  collectMinimal(tree, minimalErrors);
  return { tree, unit: tree.unit, minimalErrors };
}

export function formatTree(node: DerivationNode, indent = ""): string {
  const mark = node.error === null ? "" : `  <-- ${node.error.code}`;
  const lines = [`${indent}${node.rule}${mark}`];
  for (const c of node.children) lines.push(formatTree(c, indent + "  "));
  return lines.join("\n");
}

export function formatMinimalErrors(source: string, result: CheckResult): string {
  return result.minimalErrors
    .map((n) => {
      const text = slice(source, n.expr);
      const err = n.error!;
      const detail =
        err.code === "UNIT_MISMATCH"
          ? `cannot ${err.op} ${formatUnit(err.left)} and ${formatUnit(err.right)}`
          : err.code === "UNDEFINED_VAR"
            ? `undefined variable '${err.name}'`
            : "error";
      return `[${n.expr.start}:${n.expr.end}] '${text}' — ${detail}`;
    })
    .join("\n");
}
