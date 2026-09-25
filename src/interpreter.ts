import type { BoolExpr, Operand, PathAst, Segment } from './ast.ts';
import { PathError } from './errors.ts';
import type { Match, PathSeg } from './vm.ts';
import {
  assertDataValue,
  isTraversable,
  ownEntries,
  readIndex,
  readOwn,
} from './safeobj.ts';

/**
 * Independent slow reference interpreter. Deliberately written as plain
 * recursion directly over the AST -- no compiled plan, no work stack, and
 * its own filter evaluator -- so tests can cross-check the optimising VM
 * against an obviously-correct second implementation.
 */
export function evaluateSlow(
  ast: PathAst,
  root: unknown,
  budget: number,
): Match[] {
  const fuel = { left: budget };
  const spend = (n: number): void => {
    if (fuel.left < n) {
      throw new PathError(
        'BUDGET_EXCEEDED',
        ast.source,
        `step budget of ${budget} exhausted`,
      );
    }
    fuel.left -= n;
  };
  assertDataValue(root, ast.source);
  const out: Match[] = [];
  walk(ast.segments, 0, root, [], out, spend, ast.source);
  return out;
}

function walk(
  segments: readonly Segment[],
  at: number,
  value: unknown,
  path: readonly PathSeg[],
  out: Match[],
  spend: (n: number) => void,
  userPath: string,
): void {
  spend(1);
  assertDataValue(value, userPath);
  if (at >= segments.length) {
    spend(1);
    out.push({ value, path });
    return;
  }
  const seg = segments[at]!;
  const down = (v: unknown, key: PathSeg): void => {
    spend(1);
    walk(segments, at + 1, v, [...path, key], out, spend, userPath);
  };
  switch (seg.kind) {
    case 'key': {
      const r = readOwn(value, seg.name, userPath);
      if (r.found) down(r.value, seg.name);
      return;
    }
    case 'index': {
      const r = readIndex(value, seg.index, userPath);
      if (r.found) down(r.value, seg.index);
      return;
    }
    case 'wildcard': {
      for (const [key, v] of ownEntries(value)) {
        down(v, Array.isArray(value) ? Number(key) : key);
      }
      return;
    }
    case 'descend': {
      const r = readOwn(value, seg.name, userPath);
      if (r.found) down(r.value, seg.name);
      for (const [key, child] of ownEntries(value)) {
        if (isTraversable(child)) {
          spend(1);
          const ps: PathSeg = Array.isArray(value) ? Number(key) : key;
          walk(segments, at, child, [...path, ps], out, spend, userPath);
        }
      }
      return;
    }
    case 'filter': {
      for (const [key, candidate] of ownEntries(value)) {
        spend(1);
        if (slowFilter(seg.expr, candidate, userPath, spend)) {
          down(candidate, Array.isArray(value) ? Number(key) : key);
        }
      }
      return;
    }
  }
}

const NO_VALUE: unique symbol = Symbol('no-value');

function slowOperand(
  operand: Operand,
  self: unknown,
  userPath: string,
): unknown {
  if (operand.kind === 'literal') return operand.value;
  let cur: unknown = self;
  for (const step of operand.steps) {
    const r =
      step.kind === 'index'
        ? readIndex(cur, step.index, userPath)
        : readOwn(cur, step.name, userPath);
    if (!r.found) return NO_VALUE;
    cur = r.value;
  }
  return cur;
}

function scalarEq(a: unknown, b: unknown): boolean {
  if (a === NO_VALUE || b === NO_VALUE) return false;
  const ta = typeof a;
  if (ta !== typeof b) return false;
  if (ta === 'number' || ta === 'string' || ta === 'boolean') return a === b;
  return a === null && b === null;
}

function slowFilter(
  expr: BoolExpr,
  self: unknown,
  userPath: string,
  spend: (n: number) => void,
): boolean {
  spend(1);
  switch (expr.kind) {
    case 'or': {
      for (const item of expr.items) {
        if (slowFilter(item, self, userPath, spend)) return true;
      }
      return false;
    }
    case 'and': {
      for (const item of expr.items) {
        if (!slowFilter(item, self, userPath, spend)) return false;
      }
      return true;
    }
    case 'not':
      return !slowFilter(expr.item, self, userPath, spend);
    case 'in': {
      const v = slowOperand(expr.target, self, userPath);
      for (const lit of expr.set) {
        if (scalarEq(v, lit)) return true;
      }
      return false;
    }
    case 'cmp': {
      const l = slowOperand(expr.left, self, userPath);
      const r = slowOperand(expr.right, self, userPath);
      if (expr.op === '==') return scalarEq(l, r);
      if (expr.op === '!=') {
        return l !== NO_VALUE && r !== NO_VALUE && !scalarEq(l, r);
      }
      if (!Number.isInteger(l) || !Number.isInteger(r)) return false;
      const a = l as number;
      const b = r as number;
      if (expr.op === '<') return a < b;
      if (expr.op === '<=') return a <= b;
      if (expr.op === '>') return a > b;
      return a >= b;
    }
  }
}
