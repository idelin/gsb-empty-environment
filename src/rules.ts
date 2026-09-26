/**
 * Rule (relation) AST for instance validation. Supported rule kinds —
 * and only these — are:
 *
 *   eq          field equality between two operands
 *   cmp         order relation (lt/le/gt/ge) between two int operands
 *   requiredIf  conditional requiredness: condition => target must exist
 *   sum         sum of an int list (optionally of an item field) vs an operand
 *   when        conditional branch: condition => nested rule
 *
 * Unknown rule kinds are rejected as "unsupported-feature".
 */

import { formatPath } from "./paths.ts";
import { makeIssue, type Issue } from "./issues.ts";
import type { CompileResult } from "./schema.ts";

export type Operand =
  | { readonly tag: "path"; readonly path: string }
  | { readonly tag: "const"; readonly value: number | string | boolean };

export type CondOp = "eq" | "ne" | "lt" | "le" | "gt" | "ge";

export interface Condition {
  readonly op: CondOp;
  readonly left: Operand;
  readonly right: Operand;
}

export type Rule =
  | { readonly kind: "eq"; readonly left: Operand; readonly right: Operand }
  | { readonly kind: "cmp"; readonly op: "lt" | "le" | "gt" | "ge"; readonly left: Operand; readonly right: Operand }
  | { readonly kind: "requiredIf"; readonly condition: Condition; readonly target: string }
  | {
      readonly kind: "sum";
      readonly list: string;
      readonly field: string | null;
      readonly op: CondOp;
      readonly value: Operand;
    }
  | { readonly kind: "when"; readonly condition: Condition; readonly then: Rule };

const COND_OPS: readonly string[] = ["eq", "ne", "lt", "le", "gt", "ge"];
const CMP_OPS: readonly string[] = ["lt", "le", "gt", "ge"];

export function compileRules(raw: unknown): CompileResult<Rule[]> {
  if (!Array.isArray(raw)) {
    return {
      ok: false,
      issues: [
        makeIssue({ kind: "invalid-rule", rulePath: "/rules", message: "rules document must be a list" }),
      ],
    };
  }
  const issues: Issue[] = [];
  const rules: Rule[] = [];
  let failed = false;
  raw.forEach((item, index) => {
    const rule = compileRule(item, ["rules", index], issues);
    if (rule === null) failed = true;
    else rules.push(rule);
  });
  if (failed || issues.length > 0) return { ok: false, issues };
  return { ok: true, value: rules };
}

function compileRule(raw: unknown, rp: (string | number)[], issues: Issue[]): Rule | null {
  const here = formatPath(rp);
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    issues.push(makeIssue({ kind: "invalid-rule", rulePath: here, message: "rule must be a plain object" }));
    return null;
  }
  const rec = raw as Record<string, unknown>;
  switch (rec.kind) {
    case "eq": {
      const left = compileOperand(rec.left, [...rp, "left"], issues);
      const right = compileOperand(rec.right, [...rp, "right"], issues);
      return left === null || right === null ? null : { kind: "eq", left, right };
    }
    case "cmp": {
      if (typeof rec.op !== "string" || !CMP_OPS.includes(rec.op)) {
        issues.push(
          makeIssue({
            kind: "invalid-rule",
            rulePath: formatPath([...rp, "op"]),
            message: `cmp op must be one of ${CMP_OPS.join("/")}, got ${String(rec.op)}`,
          }),
        );
        return null;
      }
      const left = compileOperand(rec.left, [...rp, "left"], issues);
      const right = compileOperand(rec.right, [...rp, "right"], issues);
      if (left === null || right === null) return null;
      return { kind: "cmp", op: rec.op as "lt" | "le" | "gt" | "ge", left, right };
    }
    case "requiredIf": {
      const condition = compileCondition(rec.condition, [...rp, "condition"], issues);
      if (typeof rec.target !== "string") {
        issues.push(
          makeIssue({
            kind: "invalid-rule",
            rulePath: formatPath([...rp, "target"]),
            message: "requiredIf target must be a path string",
          }),
        );
        return null;
      }
      return condition === null ? null : { kind: "requiredIf", condition, target: rec.target };
    }
    case "sum": {
      if (typeof rec.list !== "string") {
        issues.push(
          makeIssue({
            kind: "invalid-rule",
            rulePath: formatPath([...rp, "list"]),
            message: "sum list must be a path string",
          }),
        );
        return null;
      }
      if (rec.field !== undefined && typeof rec.field !== "string") {
        issues.push(
          makeIssue({
            kind: "invalid-rule",
            rulePath: formatPath([...rp, "field"]),
            message: "sum field must be a string when present",
          }),
        );
        return null;
      }
      if (typeof rec.op !== "string" || !COND_OPS.includes(rec.op)) {
        issues.push(
          makeIssue({
            kind: "invalid-rule",
            rulePath: formatPath([...rp, "op"]),
            message: `sum op must be one of ${COND_OPS.join("/")}, got ${String(rec.op)}`,
          }),
        );
        return null;
      }
      const value = compileOperand(rec.value, [...rp, "value"], issues);
      if (value === null) return null;
      return {
        kind: "sum",
        list: rec.list,
        field: typeof rec.field === "string" ? rec.field : null,
        op: rec.op as CondOp,
        value,
      };
    }
    case "when": {
      const condition = compileCondition(rec.condition, [...rp, "condition"], issues);
      const then = compileRule(rec.then, [...rp, "then"], issues);
      return condition === null || then === null ? null : { kind: "when", condition, then };
    }
    default:
      issues.push(
        makeIssue({
          kind: "unsupported-feature",
          rulePath: formatPath([...rp, "kind"]),
          message: `rule kind ${JSON.stringify(rec.kind)} is outside the supported subset; it is rejected, not ignored`,
        }),
      );
      return null;
  }
}

function compileCondition(raw: unknown, rp: (string | number)[], issues: Issue[]): Condition | null {
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    issues.push(
      makeIssue({ kind: "invalid-rule", rulePath: formatPath(rp), message: "condition must be a plain object" }),
    );
    return null;
  }
  const rec = raw as Record<string, unknown>;
  if (typeof rec.op !== "string" || !COND_OPS.includes(rec.op)) {
    issues.push(
      makeIssue({
        kind: "invalid-rule",
        rulePath: formatPath([...rp, "op"]),
        message: `condition op must be one of ${COND_OPS.join("/")}, got ${String(rec.op)}`,
      }),
    );
    return null;
  }
  const left = compileOperand(rec.left, [...rp, "left"], issues);
  const right = compileOperand(rec.right, [...rp, "right"], issues);
  if (left === null || right === null) return null;
  return { op: rec.op as CondOp, left, right };
}

function compileOperand(raw: unknown, rp: (string | number)[], issues: Issue[]): Operand | null {
  const here = formatPath(rp);
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    issues.push(
      makeIssue({ kind: "invalid-rule", rulePath: here, message: 'operand must be { "path": ... } or { "const": ... }' }),
    );
    return null;
  }
  const rec = raw as Record<string, unknown>;
  const keys = Object.keys(rec);
  if (keys.length === 1 && keys[0] === "path" && typeof rec.path === "string") {
    return { tag: "path", path: rec.path };
  }
  if (
    keys.length === 1 &&
    keys[0] === "const" &&
    (typeof rec.const === "number" || typeof rec.const === "string" || typeof rec.const === "boolean")
  ) {
    return { tag: "const", value: rec.const };
  }
  issues.push(
    makeIssue({ kind: "invalid-rule", rulePath: here, message: 'operand must be { "path": ... } or { "const": ... }' }),
  );
  return null;
}
