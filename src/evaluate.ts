/**
 * Validation pipeline. Phases run in a strict order and earlier failures
 * gate later phases:
 *
 *   1. coverage — every path a rule references must be described by the
 *      schema, otherwise type checking cannot be guaranteed.
 *   2. type     — type and existence checking of the instance. Type/range
 *      errors block relation evaluation entirely (类型检查先于关系计算).
 *   3. relation — rules are evaluated with three-valued logic: a missing
 *      operand makes a relation "unknown" (reported), never false, and
 *      missing values are never zero-filled.
 */

import { formatPath, parsePath } from "./paths.ts";
import { deepEqual, lookup, typeOf, type Tri } from "./values.ts";
import { makeIssue, type Issue, type RelatedPath } from "./issues.ts";
import { coversPath, schemaAt, typeCheck, type Schema } from "./schema.ts";
import type { Condition, Operand, Rule } from "./rules.ts";

export interface ValidationResult {
  readonly issues: readonly Issue[];
  /** The last phase that ran; later phases were skipped. */
  readonly phase: "coverage" | "type" | "relation";
}

export function validate(instance: unknown, schema: Schema, rules: readonly Rule[]): ValidationResult {
  const coverageIssues = checkCoverage(schema, rules);
  if (coverageIssues.length > 0) return { issues: coverageIssues, phase: "coverage" };

  const typeIssues = typeCheck(instance, schema);
  const blocking = typeIssues.some((i) => i.kind === "type" || i.kind === "range");
  if (blocking) return { issues: typeIssues, phase: "type" };

  const issues: Issue[] = [...typeIssues];
  rules.forEach((rule, index) => evalRule(instance, rule, ["rules", index], issues));
  return { issues, phase: "relation" };
}

function checkCoverage(schema: Schema, rules: readonly Rule[]): Issue[] {
  const issues: Issue[] = [];
  const requirePath = (path: string, rp: (string | number)[]): void => {
    if (!coversPath(schema, parsePath(path))) {
      issues.push(
        makeIssue({
          kind: "uncovered-path",
          instancePath: path,
          rulePath: formatPath(rp),
          message: `rule references ${path}, which the schema does not describe; type checking cannot be guaranteed`,
        }),
      );
    }
  };
  const visitOperand = (operand: Operand, rp: (string | number)[]): void => {
    if (operand.tag === "path") requirePath(operand.path, rp);
  };
  const visitCondition = (condition: Condition, rp: (string | number)[]): void => {
    visitOperand(condition.left, [...rp, "left"]);
    visitOperand(condition.right, [...rp, "right"]);
  };
  const visit = (rule: Rule, rp: (string | number)[]): void => {
    switch (rule.kind) {
      case "eq":
      case "cmp":
        visitOperand(rule.left, [...rp, "left"]);
        visitOperand(rule.right, [...rp, "right"]);
        return;
      case "requiredIf":
        visitCondition(rule.condition, [...rp, "condition"]);
        requirePath(rule.target, [...rp, "target"]);
        return;
      case "sum": {
        requirePath(rule.list, [...rp, "list"]);
        if (rule.field !== null && coversPath(schema, parsePath(rule.list))) {
          const listSchema = schemaAt(schema, parsePath(rule.list));
          const items = listSchema !== null && listSchema.kind === "list" ? listSchema.items : null;
          if (items === null || items.kind !== "object" || !(rule.field in items.fields)) {
            issues.push(
              makeIssue({
                kind: "uncovered-path",
                instancePath: `${rule.list}/*/${rule.field}`,
                rulePath: formatPath([...rp, "field"]),
                message: `sum field "${rule.field}" is not described by the list item schema`,
              }),
            );
          }
        }
        visitOperand(rule.value, [...rp, "value"]);
        return;
      }
      case "when":
        visitCondition(rule.condition, [...rp, "condition"]);
        visit(rule.then, [...rp, "then"]);
        return;
    }
  };
  rules.forEach((rule, index) => visit(rule, ["rules", index]));
  return issues;
}

type EvalValue =
  | { readonly status: "ok"; readonly value: unknown }
  | { readonly status: "missing"; readonly path: string };

function evalOperand(instance: unknown, operand: Operand): EvalValue {
  if (operand.tag === "const") return { status: "ok", value: operand.value };
  const hit = lookup(instance, parsePath(operand.path));
  return hit.present ? { status: "ok", value: hit.value } : { status: "missing", path: operand.path };
}

function operandInstancePath(operand: Operand): string | undefined {
  return operand.tag === "path" ? operand.path : undefined;
}

function operandRelated(left: Operand, right: Operand): RelatedPath[] {
  const related: RelatedPath[] = [];
  const leftPath = operandInstancePath(left);
  const rightPath = operandInstancePath(right);
  if (leftPath !== undefined) related.push({ instancePath: leftPath, note: "left operand" });
  if (rightPath !== undefined) related.push({ instancePath: rightPath, note: "right operand" });
  return related;
}

function cmpNumbers(op: "lt" | "le" | "gt" | "ge" | "eq" | "ne", a: number, b: number): boolean {
  switch (op) {
    case "lt":
      return a < b;
    case "le":
      return a <= b;
    case "gt":
      return a > b;
    case "ge":
      return a >= b;
    case "eq":
      return a === b;
    case "ne":
      return a !== b;
  }
}

interface CondOutcome {
  readonly tri: Tri;
  /** True when an issue was already emitted (defensive type error). */
  readonly reported: boolean;
  readonly missingPaths: readonly string[];
}

function evalCondition(
  instance: unknown,
  condition: Condition,
  rp: (string | number)[],
  out: Issue[],
): CondOutcome {
  const left = evalOperand(instance, condition.left);
  const right = evalOperand(instance, condition.right);
  if (left.status !== "ok" || right.status !== "ok") {
    const missing = [left, right].flatMap((e) => (e.status === "missing" ? [e.path] : []));
    return { tri: "unknown", reported: false, missingPaths: missing };
  }
  const a = left.value;
  const b = right.value;
  if (condition.op === "eq" || condition.op === "ne") {
    const equal = deepEqual(a, b);
    return { tri: condition.op === "eq" ? (equal ? "true" : "false") : equal ? "false" : "true", reported: false, missingPaths: [] };
  }
  if (typeof a !== "number" || typeof b !== "number") {
    // Defensive: phase 1 (coverage + type check) should make this unreachable.
    out.push(
      makeIssue({
        kind: "type",
        instancePath: operandInstancePath(condition.left) ?? "",
        rulePath: formatPath(rp),
        message: `ordering requires numbers, got ${typeOf(a)} and ${typeOf(b)}`,
      }),
    );
    return { tri: "unknown", reported: true, missingPaths: [] };
  }
  return { tri: cmpNumbers(condition.op, a, b) ? "true" : "false", reported: false, missingPaths: [] };
}

function reportUnknownCondition(
  out: Issue[],
  rp: (string | number)[],
  outcome: CondOutcome,
  ruleDescription: string,
): void {
  if (outcome.reported) return;
  out.push(
    makeIssue({
      kind: "unknown-condition",
      instancePath: outcome.missingPaths[0] ?? "",
      rulePath: formatPath(rp),
      message: `condition of ${ruleDescription} is unknown (missing operand); it is not treated as false and the rule is not silently skipped`,
      related: outcome.missingPaths.slice(1).map((p) => ({ instancePath: p })),
    }),
  );
}

function reportUnknownOperand(
  out: Issue[],
  rp: (string | number)[],
  evals: readonly EvalValue[],
  message: string,
): void {
  const missing = evals.flatMap((e) => (e.status === "missing" ? [e.path] : []));
  out.push(
    makeIssue({
      kind: "unknown-operand",
      instancePath: missing[0] ?? "",
      rulePath: formatPath(rp),
      message,
      related: missing.slice(1).map((p) => ({ instancePath: p })),
    }),
  );
}

function evalRule(instance: unknown, rule: Rule, rp: (string | number)[], out: Issue[]): void {
  const here = formatPath(rp);
  switch (rule.kind) {
    case "eq": {
      const left = evalOperand(instance, rule.left);
      const right = evalOperand(instance, rule.right);
      if (left.status !== "ok" || right.status !== "ok") {
        reportUnknownOperand(out, rp, [left, right], "equality operand is missing; the relation is unknown");
        return;
      }
      if (!deepEqual(left.value, right.value)) {
        out.push(
          makeIssue({
            kind: "relation",
            instancePath: operandInstancePath(rule.left) ?? "",
            rulePath: here,
            message: `equality violated: ${JSON.stringify(left.value)} !== ${JSON.stringify(right.value)}`,
            related: operandRelated(rule.left, rule.right),
          }),
        );
      }
      return;
    }
    case "cmp": {
      const left = evalOperand(instance, rule.left);
      const right = evalOperand(instance, rule.right);
      if (left.status !== "ok" || right.status !== "ok") {
        reportUnknownOperand(out, rp, [left, right], "comparison operand is missing; the relation is unknown");
        return;
      }
      if (typeOf(left.value) !== "int" || typeOf(right.value) !== "int") {
        out.push(
          makeIssue({
            kind: "type",
            instancePath: operandInstancePath(rule.left) ?? "",
            rulePath: here,
            message: `comparison requires ints, got ${typeOf(left.value)} and ${typeOf(right.value)}`,
          }),
        );
        return;
      }
      const a = left.value as number;
      const b = right.value as number;
      if (!cmpNumbers(rule.op, a, b)) {
        out.push(
          makeIssue({
            kind: "relation",
            instancePath: operandInstancePath(rule.left) ?? "",
            rulePath: here,
            message: `order violated: ${a} does not satisfy ${rule.op} ${b}`,
            related: operandRelated(rule.left, rule.right),
          }),
        );
      }
      return;
    }
    case "requiredIf": {
      const outcome = evalCondition(instance, rule.condition, [...rp, "condition"], out);
      if (outcome.tri === "unknown") {
        reportUnknownCondition(out, rp, outcome, `requiredIf (target ${rule.target})`);
        return;
      }
      if (outcome.tri === "false") return;
      const hit = lookup(instance, parsePath(rule.target));
      if (!hit.present) {
        out.push(
          makeIssue({
            kind: "missing-required",
            instancePath: rule.target,
            rulePath: here,
            message: `conditionally required field ${rule.target} is missing`,
            related: [{ rulePath: formatPath([...rp, "condition"]), note: "triggering condition" }],
          }),
        );
      }
      return;
    }
    case "sum": {
      const listHit = lookup(instance, parsePath(rule.list));
      if (!listHit.present) {
        out.push(
          makeIssue({
            kind: "unknown-operand",
            instancePath: rule.list,
            rulePath: here,
            message: `list ${rule.list} is missing; its sum is unknown (a missing list is not the empty list and is never zero-filled)`,
          }),
        );
        return;
      }
      if (!Array.isArray(listHit.value)) {
        out.push(
          makeIssue({
            kind: "type",
            instancePath: rule.list,
            rulePath: here,
            message: `expected list, got ${typeOf(listHit.value)}`,
          }),
        );
        return;
      }
      const items = listHit.value as unknown[];
      const listSegs = parsePath(rule.list);
      let total = 0; // the empty list sums to 0
      for (let index = 0; index < items.length; index++) {
        let element = items[index];
        let elementPath = formatPath([...listSegs, String(index)]);
        if (rule.field !== null) {
          if (typeOf(element) !== "object") {
            out.push(
              makeIssue({
                kind: "type",
                instancePath: elementPath,
                rulePath: here,
                message: `expected object with field "${rule.field}", got ${typeOf(element)}`,
              }),
            );
            return;
          }
          const rec = element as Record<string, unknown>;
          if (!Object.prototype.hasOwnProperty.call(rec, rule.field)) {
            out.push(
              makeIssue({
                kind: "unknown-operand",
                instancePath: formatPath([...listSegs, String(index), rule.field]),
                rulePath: here,
                message: `item field "${rule.field}" is missing; the sum is unknown (no zero-fill)`,
              }),
            );
            return;
          }
          element = rec[rule.field];
          elementPath = formatPath([...listSegs, String(index), rule.field]);
        }
        if (typeOf(element) !== "int") {
          out.push(
            makeIssue({
              kind: "type",
              instancePath: elementPath,
              rulePath: here,
              message: `sum requires int elements, got ${typeOf(element)}`,
            }),
          );
          return;
        }
        total += element as number;
      }
      const bound = evalOperand(instance, rule.value);
      if (bound.status !== "ok") {
        reportUnknownOperand(out, rp, [bound], `sum bound ${bound.path} is missing; the relation is unknown`);
        return;
      }
      if (typeOf(bound.value) !== "int") {
        out.push(
          makeIssue({
            kind: "type",
            instancePath: operandInstancePath(rule.value) ?? "",
            rulePath: here,
            message: `sum bound must be an int, got ${typeOf(bound.value)}`,
          }),
        );
        return;
      }
      const target = bound.value as number;
      if (!cmpNumbers(rule.op, total, target)) {
        out.push(
          makeIssue({
            kind: "relation",
            instancePath: rule.list,
            rulePath: here,
            message: `sum ${total} violates ${rule.op} ${target}`,
            related: [
              ...(operandInstancePath(rule.value) !== undefined
                ? [{ instancePath: operandInstancePath(rule.value)!, note: `bound = ${target}` }]
                : []),
            ],
          }),
        );
      }
      return;
    }
    case "when": {
      const outcome = evalCondition(instance, rule.condition, [...rp, "condition"], out);
      if (outcome.tri === "unknown") {
        reportUnknownCondition(out, rp, outcome, "when");
        return;
      }
      if (outcome.tri === "false") return;
      evalRule(instance, rule.then, [...rp, "then"], out);
      return;
    }
  }
}
