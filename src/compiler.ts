/**
 * Compiler: turns a parsed path expression into a resumable execution plan.
 *
 * The plan is a generator. Recursive descent, filter evaluation and result
 * emission all consume units from a single shared step budget. When the
 * caller-granted slice of steps runs out, the generator suspends; the caller
 * may grant more steps via `Execution.resume(...)`. A hard `maxTotalSteps`
 * cap bounds total work and raises `BudgetExceededError`.
 */
import type { FilterExpr, PathAst, Segment } from './ast.ts';
import { parse, type ParseLimits } from './parser.ts';
import { AccessDeniedError, BudgetExceededError } from './errors.ts';
import { getOwn, isDataContainer, ownKeys } from './safeAccess.ts';
import {
  compareValues,
  inSet,
  isTruthy,
  resolveAt,
  resolveOperand,
} from './semantics.ts';

export interface Match {
  readonly path: readonly (string | number)[];
  readonly value: unknown;
}

export const DEFAULT_MAX_STEPS = 100_000;

export interface RunOptions {
  /** Hard cap on total steps for one execution. Default 100_000. */
  maxTotalSteps?: number;
}

interface Ctx {
  readonly expression: string;
  readonly maxTotal: number;
  slice: number;   // steps granted by the caller but not yet consumed
  total: number;   // total steps consumed so far
  matches: Match[];
}

/** Consume one step; suspend (yield) when the granted slice is exhausted. */
function* tick(ctx: Ctx): Generator<void, void, void> {
  if (ctx.slice <= 0) yield; // wait for the caller to grant more steps
  ctx.total += 1;
  if (ctx.total > ctx.maxTotal) {
    throw new BudgetExceededError(
      `step budget exhausted: more than ${ctx.maxTotal} steps required`,
      ctx.expression,
    );
  }
  ctx.slice -= 1;
}

function childKey(container: unknown, key: string): string | number {
  return Array.isArray(container) ? Number(key) : key;
}

function* evalFilter(expr: FilterExpr, item: unknown, ctx: Ctx): Generator<void, boolean, void> {
  yield* tick(ctx); // every filter node consumes budget
  switch (expr.type) {
    case 'or':
      for (const e of expr.items) {
        if (yield* evalFilter(e, item, ctx)) return true;
      }
      return false;
    case 'and':
      for (const e of expr.items) {
        if (!(yield* evalFilter(e, item, ctx))) return false;
      }
      return true;
    case 'not':
      return !(yield* evalFilter(expr.expr, item, ctx));
    case 'exists':
      return resolveAt(expr.path, item).present;
    case 'truthy': {
      const r = resolveAt(expr.path, item);
      return r.present && isTruthy(r.value);
    }
    case 'compare':
      return compareValues(expr.op, resolveOperand(expr.left, item), resolveOperand(expr.right, item));
    case 'in':
      return inSet(resolveOperand(expr.left, item), expr.set);
  }
}

interface DescentEntry {
  value: unknown;
  path: (string | number)[];
  parent: DescentEntry | null;
}

function isAncestor(value: unknown, entry: DescentEntry | null): boolean {
  for (let e = entry; e !== null; e = e.parent) {
    if (e.value === value) return true;
  }
  return false;
}

function* walk(
  segments: Segment[],
  index: number,
  value: unknown,
  path: (string | number)[],
  ctx: Ctx,
): Generator<void, void, void> {
  if (index >= segments.length) {
    yield* tick(ctx); // emitting output consumes budget
    ctx.matches.push({ path: Object.freeze(path.slice()), value });
    return;
  }
  const seg = segments[index];
  yield* tick(ctx); // applying a segment consumes budget
  switch (seg.type) {
    case 'child': {
      const r = getOwn(value, seg.key);
      if (r.kind === 'denied') {
        throw new AccessDeniedError(`access to property ${JSON.stringify(seg.key)} is denied by policy`, ctx.expression);
      }
      if (r.kind === 'value') {
        yield* walk(segments, index + 1, r.value, [...path, seg.key], ctx);
      }
      return;
    }
    case 'index': {
      const r = getOwn(value, String(seg.index));
      if (r.kind === 'value') {
        yield* walk(segments, index + 1, r.value, [...path, seg.index], ctx);
      }
      return;
    }
    case 'wildcard':
    case 'filter': {
      if (!isDataContainer(value)) return;
      for (const key of ownKeys(value)) {
        yield* tick(ctx); // each enumerated child consumes budget
        const r = getOwn(value, key);
        if (r.kind !== 'value') continue;
        if (seg.type === 'filter' && !(yield* evalFilter(seg.expr, r.value, ctx))) continue;
        yield* walk(segments, index + 1, r.value, [...path, childKey(value, key)], ctx);
      }
      return;
    }
    case 'descent': {
      // Iterative DFS with an ancestor chain guard against cycles.
      const root: DescentEntry = { value, path: path.slice(), parent: null };
      const stack: DescentEntry[] = [root];
      while (stack.length > 0) {
        const entry = stack.pop() as DescentEntry;
        yield* tick(ctx); // each visited node consumes budget
        const hit = getOwn(entry.value, seg.key);
        if (hit.kind === 'value') {
          yield* walk(segments, index + 1, hit.value, [...entry.path, seg.key], ctx);
        }
        if (!isDataContainer(entry.value)) continue;
        for (const key of ownKeys(entry.value)) {
          const child = getOwn(entry.value, key);
          if (child.kind !== 'value' || !isDataContainer(child.value)) continue;
          if (isAncestor(child.value, entry)) continue; // cycle guard
          stack.push({
            value: child.value,
            path: [...entry.path, childKey(entry.value, key)],
            parent: entry,
          });
        }
      }
      return;
    }
  }
}

export class Execution {
  private readonly ctx: Ctx;
  private readonly gen: Generator<void, void, void>;
  private finished = false;

  constructor(expression: string, segments: Segment[], root: unknown, maxTotal: number) {
    this.ctx = { expression, maxTotal, slice: 0, total: 0, matches: [] };
    this.gen = walk(segments, 0, root, [], this.ctx);
  }

  /**
   * Grant `steps` more steps and continue. Returns the done flag, the matches
   * collected so far, and the total steps consumed. Throws
   * BudgetExceededError if the hard cap is exceeded.
   */
  resume(steps: number): { done: boolean; matches: Match[]; totalSteps: number } {
    if (this.finished) {
      return { done: true, matches: this.ctx.matches, totalSteps: this.ctx.total };
    }
    this.ctx.slice += steps;
    try {
      const r = this.gen.next();
      if (r.done === true) this.finished = true;
    } catch (err) {
      this.finished = true;
      throw err;
    }
    return { done: this.finished, matches: this.ctx.matches, totalSteps: this.ctx.total };
  }
}

export class CompiledPlan {
  readonly expression: string;
  readonly ast: PathAst;

  constructor(expression: string, ast: PathAst) {
    this.expression = expression;
    this.ast = ast;
  }

  /** Create a fresh, pausable execution over `root`. */
  start(root: unknown, options: RunOptions = {}): Execution {
    return new Execution(
      this.expression,
      this.ast.segments,
      root,
      options.maxTotalSteps ?? DEFAULT_MAX_STEPS,
    );
  }

  /** Run to completion (bounded by the step cap) and return all matches. */
  run(root: unknown, options: RunOptions = {}): Match[] {
    const execution = this.start(root, options);
    while (true) {
      const r = execution.resume(Number.MAX_SAFE_INTEGER);
      if (r.done) return r.matches;
    }
  }
}

/** Compile a path expression into a reusable, resumable plan. */
export function compile(expression: string, limits: Partial<ParseLimits> = {}): CompiledPlan {
  return new CompiledPlan(expression, parse(expression, limits));
}
