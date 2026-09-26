/**
 * Rule evaluator.
 *
 * Semantics contract:
 *  - Type checking happens BEFORE any relation is computed; a type mismatch
 *    is a `type-error`, never silently coerced and never zero-filled.
 *  - Missing values make relations `unknown`, never `false`. An `unknown`
 *    condition is reported, never silently skipped.
 *  - An empty list sums to 0; a missing list is `unknown` (not 0).
 *  - Every issue carries BOTH the instance path and the rule path.
 */

import { getPath, parsePath, typeOf, type Json, type TypeName } from './paths.ts';
import type { Condition, Operand, Rule } from './rules.ts';

export type Tri = 'true' | 'false' | 'unknown';

export const triNot = (t: Tri): Tri => (t === 'true' ? 'false' : t === 'false' ? 'true' : 'unknown');
export const triAnd = (a: Tri, b: Tri): Tri =>
  a === 'false' || b === 'false' ? 'false' : a === 'true' && b === 'true' ? 'true' : 'unknown';
export const triOr = (a: Tri, b: Tri): Tri =>
  a === 'true' || b === 'true' ? 'true' : a === 'false' && b === 'false' ? 'false' : 'unknown';

export interface Issue {
  severity: 'violation' | 'type-error' | 'unknown';
  ruleKind: string;
  instancePath: string;
  rulePath: string;
  relatedPaths: string[];
  message: string;
}

export interface EvalReport {
  status: 'pass' | 'fail' | 'unknown';
  issues: Issue[];
}

type Resolved =
  | { status: 'present'; value: Json; path?: string }
  | { status: 'missing'; path: string };

function resolveOperand(root: Json, op: Operand): Resolved {
  if (op.kind === 'literal') return { status: 'present', value: op.value };
  const hit: Lookup = getPath(root, op.path);
  return hit.status === 'present'
    ? { status: 'present', value: hit.value, path: op.path }
    : { status: 'missing', path: op.path };
}

function operandPath(op: Operand): string {
  return op.kind === 'field' ? op.path : '(literal)';
}

interface Ctx {
  root: Json;
  issues: Issue[];
}

function push(ctx: Ctx, issue: Omit<Issue, 'relatedPaths'> & { relatedPaths?: string[] }): void {
  ctx.issues.push({ relatedPaths: [], ...issue });
}

/**
 * Compare two operands. Type check first: ordering requires numbers on both
 * sides; equality requires matching primitive types. Missing => unknown.
 */
function compareOperands(
  ctx: Ctx,
  op: '==' | '!=' | '<' | '<=' | '>' | '>=',
  left: Operand,
  right: Operand,
  ruleKind: string,
  rulePath: string,
): Tri {
  const l = resolveOperand(ctx.root, left);
  const r = resolveOperand(ctx.root, right);
  const paths = [operandPath(left), operandPath(right)].filter((p) => p !== '(literal)');

  if (l.status === 'missing' || r.status === 'missing') {
    const missingPath = l.status === 'missing' ? l.path : (r as { path: string }).path;
    push(ctx, {
      severity: 'unknown',
      ruleKind,
      instancePath: missingPath,
      rulePath,
      relatedPaths: paths,
      message: `cannot compare: value missing at ${missingPath} (missing is not false, not zero)`,
    });
    return 'unknown';
  }

  const lt = typeOf(l.value);
  const rt = typeOf(r.value);
  const ordering = op !== '==' && op !== '!=';
  const typeOk = ordering
    ? lt === 'number' && rt === 'number'
    : lt === rt && (lt === 'number' || lt === 'string' || lt === 'boolean');

  if (!typeOk) {
    push(ctx, {
      severity: 'type-error',
      ruleKind,
      instancePath: paths[0] ?? '(literal)',
      rulePath,
      relatedPaths: paths,
      message: ordering
        ? `ordering '${op}' requires numbers, got ${lt} and ${rt}`
        : `equality '${op}' requires matching primitive types, got ${lt} and ${rt}`,
    });
    return 'unknown';
  }

  const a = l.value as number | string | boolean;
  const b = r.value as number | string | boolean;
  switch (op) {
    case '==': return a === b ? 'true' : 'false';
    case '!=': return a !== b ? 'true' : 'false';
    case '<':  return (a as number) <  (b as number) ? 'true' : 'false';
    case '<=': return (a as number) <= (b as number) ? 'true' : 'false';
    case '>':  return (a as number) >  (b as number) ? 'true' : 'false';
    case '>=': return (a as number) >= (b as number) ? 'true' : 'false';
  }
}

function evalCondition(ctx: Ctx, cond: Condition, rulePath: string): Tri {
  switch (cond.kind) {
    case 'compare':
      return compareOperands(ctx, cond.op, cond.left, cond.right, 'condition', rulePath);
    case 'exists':
      return getPath(ctx.root, cond.path).status === 'present' ? 'true' : 'false';
    case 'and': {
      let acc: Tri = 'true';
      for (const c of cond.all) acc = triAnd(acc, evalCondition(ctx, c, rulePath));
      return acc;
    }
    case 'or': {
      let acc: Tri = 'false';
      for (const c of cond.any) acc = triOr(acc, evalCondition(ctx, c, rulePath));
      return acc;
    }
    case 'not':
      return triNot(evalCondition(ctx, cond.cond, rulePath));
  }
}

function evalRule(ctx: Ctx, rule: Rule, rulePath: string): void {
  switch (rule.kind) {
    case 'equals': {
      const t = compareOperands(ctx, '==', rule.left, rule.right, rule.kind, rulePath);
      if (t === 'false') {
        push(ctx, {
          severity: 'violation',
          ruleKind: rule.kind,
          instancePath: operandPath(rule.left),
          rulePath,
          relatedPaths: [operandPath(rule.left), operandPath(rule.right)].filter((p) => p !== '(literal)'),
          message: `equality violated between ${operandPath(rule.left)} and ${operandPath(rule.right)}`,
        });
      }
      return;
    }
    case 'compare': {
      const t = compareOperands(ctx, rule.op, rule.left, rule.right, rule.kind, rulePath);
      if (t === 'false') {
        // Both endpoint paths are reported (e.g. end < start names both sides).
        push(ctx, {
          severity: 'violation',
          ruleKind: rule.kind,
          instancePath: operandPath(rule.left),
          rulePath,
          relatedPaths: [operandPath(rule.left), operandPath(rule.right)].filter((p) => p !== '(literal)'),
          message: `relation '${rule.op}' violated between ${operandPath(rule.left)} and ${operandPath(rule.right)}`,
        });
      }
      return;
    }
    case 'requiredIf': {
      const cond = evalCondition(ctx, rule.when, `${rulePath}/when`);
      if (cond === 'unknown') {
        // An unknown condition is surfaced, never treated as false.
        push(ctx, {
          severity: 'unknown',
          ruleKind: rule.kind,
          instancePath: rule.path,
          rulePath: `${rulePath}/when`,
          message: `condition is unknown; requirement for ${rule.path} cannot be decided`,
        });
        return;
      }
      if (cond === 'true' && getPath(ctx.root, rule.path).status === 'missing') {
        push(ctx, {
          severity: 'violation',
          ruleKind: rule.kind,
          instancePath: rule.path,
          rulePath,
          message: `conditionally required field ${rule.path} is missing`,
        });
      }
      return;
    }
    case 'sum': {
      const hit = getPath(ctx.root, rule.listPath);
      if (hit.status === 'missing') {
        // A missing list is NOT the same as an empty list: unknown, not 0.
        push(ctx, {
          severity: 'unknown',
          ruleKind: rule.kind,
          instancePath: rule.listPath,
          rulePath,
          message: `list missing at ${rule.listPath}; sum is unknown (missing list is not 0)`,
        });
        return;
      }
      if (!Array.isArray(hit.value)) {
        push(ctx, {
          severity: 'type-error',
          ruleKind: rule.kind,
          instancePath: rule.listPath,
          rulePath,
          message: `expected a list at ${rule.listPath}, got ${typeOf(hit.value)}`,
        });
        return;
      }
      let total = 0;
      let badItem = false;
      hit.value.forEach((item, i) => {
        const base = `${rule.listPath}/${i}`;
        let v: Json = item;
        let itemPath = base;
        if (rule.itemPath !== undefined) {
          const sub = getPath(item, rule.itemPath);
          itemPath = `${base}${rule.itemPath}`;
          if (sub.status === 'missing') {
            push(ctx, {
              severity: 'unknown',
              ruleKind: rule.kind,
              instancePath: itemPath,
              rulePath,
              message: `item field missing at ${itemPath}; sum is unknown (no zero-fill)`,
            });
            badItem = true;
            return;
          }
          v = sub.value;
        }
        if (typeof v !== 'number') {
          push(ctx, {
            severity: 'type-error',
            ruleKind: rule.kind,
            instancePath: itemPath,
            rulePath,
            message: `sum requires numbers, got ${typeOf(v)} at ${itemPath}`,
          });
          badItem = true;
          return;
        }
        total += v;
      });
      if (badItem) return;
      // Empty list sums to 0 by definition.
      const ok =
        rule.op === '==' ? total === rule.limit :
        rule.op === '<=' ? total <= rule.limit :
        total >= rule.limit;
      if (!ok) {
        push(ctx, {
          severity: 'violation',
          ruleKind: rule.kind,
          instancePath: rule.listPath,
          rulePath,
          message: `sum ${total} does not satisfy '${rule.op} ${rule.limit}'`,
        });
      }
      return;
    }
    case 'typeIs': {
      const hit = getPath(ctx.root, rule.path);
      if (hit.status === 'missing') {
        push(ctx, {
          severity: 'unknown',
          ruleKind: rule.kind,
          instancePath: rule.path,
          rulePath,
          message: `cannot check type of missing value at ${rule.path}`,
        });
        return;
      }
      const actual: TypeName = typeOf(hit.value);
      if (actual !== rule.type) {
        push(ctx, {
          severity: 'violation',
          ruleKind: rule.kind,
          instancePath: rule.path,
          rulePath,
          message: `expected type ${rule.type}, got ${actual}`,
        });
      }
      return;
    }
  }
}

export function evaluate(instance: Json, rules: Rule[]): EvalReport {
  const ctx: Ctx = { root: instance, issues: [] };
  rules.forEach((rule, i) => evalRule(ctx, rule, `/rules/${i}`));
  const status = ctx.issues.some((i) => i.severity !== 'unknown')
    ? 'fail'
    : ctx.issues.length > 0
      ? 'unknown'
      : 'pass';
  return { status, issues: ctx.issues };
}

export { parsePath };
