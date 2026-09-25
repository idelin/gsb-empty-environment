import type { BoolExpr, Literal, Operand } from './ast.ts';
import { PathError } from './errors.ts';
import type { Plan } from './plan.ts';
import {
  assertDataValue,
  isTraversable,
  ownEntries,
  readIndex,
  readOwn,
} from './safeobj.ts';

export type PathSeg = string | number;

export interface Match {
  readonly value: unknown;
  /** Concrete path (evidence) from the document root to this match. */
  readonly path: readonly PathSeg[];
}

const MAX_RECORDED_READS = 512;

interface Task {
  readonly value: unknown;
  readonly segIndex: number;
  readonly path: readonly PathSeg[];
}

/**
 * Pausable, budgeted executor for a compiled Plan.
 *
 * Budget accounting (one shared counter):
 * - every task popped from the work stack costs 1;
 * - every node produced by key/index/wildcard/descend costs 1;
 * - every filter evaluation costs the statically compiled expression cost;
 * - every emitted match costs 1.
 *
 * `run(maxSteps)` pauses cooperatively after `maxSteps` micro-steps and can
 * be resumed; exceeding the total budget throws BUDGET_EXCEEDED instead.
 */
export class PlanCursor {
  private readonly stack: Task[] = [];
  private readonly out: Match[] = [];
  /** Concrete paths of every node visited (cache-invalidation evidence). */
  readonly reads: PathSeg[][] = [];
  readsTruncated = false;
  private usedSteps = 0;
  readonly plan: Plan;
  readonly budget: number;

  constructor(plan: Plan, root: unknown, budget: number) {
    this.plan = plan;
    this.budget = budget;
    assertDataValue(root, plan.source);
    this.stack.push({ value: root, segIndex: 0, path: [] });
  }

  get stepsUsed(): number {
    return this.usedSteps;
  }

  get done(): boolean {
    return this.stack.length === 0;
  }

  get pendingMatches(): number {
    return this.out.length;
  }

  private charge(n: number): void {
    if (this.usedSteps + n > this.budget) {
      throw new PathError(
        'BUDGET_EXCEEDED',
        this.plan.source,
        `step budget of ${this.budget} exhausted`,
      );
    }
    this.usedSteps += n;
  }

  private recordRead(path: readonly PathSeg[]): void {
    if (this.reads.length < MAX_RECORDED_READS) {
      this.reads.push([...path]);
    } else {
      this.readsTruncated = true;
    }
  }

  /** Drain and return the matches produced so far. */
  takeMatches(): Match[] {
    return this.out.splice(0, this.out.length);
  }

  /**
   * Run until the plan completes or `maxSteps` micro-steps were executed.
   * Returns true when the plan is finished.
   */
  run(maxSteps = Number.POSITIVE_INFINITY): boolean {
    let remaining = maxSteps;
    while (this.stack.length > 0 && remaining > 0) {
      this.stepOnce();
      remaining--;
    }
    return this.stack.length === 0;
  }

  /** Execute exactly one micro-step (one task). */
  stepOnce(): void {
    const task = this.stack.pop();
    if (!task) return;
    this.charge(1);
    this.recordRead(task.path);
    assertDataValue(task.value, this.plan.source);

    const op = this.plan.ops[task.segIndex];
    if (op === undefined) {
      // Past the last segment: emit this node as a match.
      this.charge(1);
      this.out.push({ value: task.value, path: task.path });
      return;
    }

    const next = task.segIndex + 1;
    const push = (value: unknown, key: PathSeg): void => {
      this.charge(1);
      this.stack.push({ value, segIndex: next, path: [...task.path, key] });
    };

    switch (op.op) {
      case 'key': {
        const r = readOwn(task.value, op.name, this.plan.source);
        if (r.found) push(r.value, op.name);
        break;
      }
      case 'index': {
        const r = readIndex(task.value, op.index, this.plan.source);
        if (r.found) push(r.value, op.index);
        break;
      }
      case 'wildcard': {
        // Push in reverse so the LIFO stack yields enumeration order.
        const entries = ownEntries(task.value);
        for (let i = entries.length - 1; i >= 0; i--) {
          const [key, value] = entries[i]!;
          const seg: PathSeg = Array.isArray(task.value) ? Number(key) : key;
          push(value, seg);
        }
        break;
      }
      case 'descend': {
        // Visit self and every traversable descendant (DFS); whenever a node
        // holds the target key as own data, continue from its value.
        // Children are pushed in reverse to preserve enumeration order, and
        // carry their real path so matches keep precise path evidence.
        const entries = ownEntries(task.value);
        for (let i = entries.length - 1; i >= 0; i--) {
          const [key, child] = entries[i]!;
          if (isTraversable(child)) {
            this.charge(1);
            const seg: PathSeg = Array.isArray(task.value) ? Number(key) : key;
            this.stack.push({
              value: child,
              segIndex: task.segIndex,
              path: [...task.path, seg],
            });
          }
        }
        const r = readOwn(task.value, op.name, this.plan.source);
        if (r.found) push(r.value, op.name);
        break;
      }
      case 'filter': {
        const entries = ownEntries(task.value);
        const kept: Array<[string, unknown]> = [];
        for (const [key, candidate] of entries) {
          this.charge(op.cost);
          if (evalFilter(op.expr, candidate, this.plan.source)) {
            kept.push([key, candidate]);
          }
        }
        for (let i = kept.length - 1; i >= 0; i--) {
          const [key, candidate] = kept[i]!;
          const seg: PathSeg = Array.isArray(task.value) ? Number(key) : key;
          push(candidate, seg);
        }
        break;
      }
    }
  }
}

/** Convenience: run a plan to completion within `budget` steps. */
export function executePlan(plan: Plan, root: unknown, budget: number): Match[] {
  const cursor = new PlanCursor(plan, root, budget);
  cursor.run();
  return cursor.takeMatches();
}

/** Missing values propagate: every comparison involving them is false. */
const MISSING: unique symbol = Symbol('missing');
type MaybeValue = unknown;

function evalOperand(
  operand: Operand,
  self: unknown,
  userPath: string,
): MaybeValue {
  if (operand.kind === 'literal') return operand.value;
  let cur: unknown = self;
  for (const step of operand.steps) {
    const r =
      step.kind === 'index'
        ? readIndex(cur, step.index, userPath)
        : readOwn(cur, step.name, userPath);
    if (!r.found) return MISSING;
    cur = r.value;
  }
  return cur;
}

function literalEquals(a: unknown, b: unknown): boolean {
  if (a === MISSING || b === MISSING) return false;
  if (typeof a === 'number' && typeof b === 'number') return a === b;
  if (typeof a === 'string' && typeof b === 'string') return a === b;
  if (typeof a === 'boolean' && typeof b === 'boolean') return a === b;
  if (a === null && b === null) return true;
  return false;
}

/** Filter evaluation for the compiled engine (integer/boolean policy only). */
export function evalFilter(expr: BoolExpr, self: unknown, userPath: string): boolean {
  switch (expr.kind) {
    case 'or':
      return expr.items.some((e) => evalFilter(e, self, userPath));
    case 'and':
      return expr.items.every((e) => evalFilter(e, self, userPath));
    case 'not':
      return !evalFilter(expr.item, self, userPath);
    case 'in': {
      const v = evalOperand(expr.target, self, userPath);
      return expr.set.some((lit: Literal) => literalEquals(v, lit));
    }
    case 'cmp': {
      const l = evalOperand(expr.left, self, userPath);
      const r = evalOperand(expr.right, self, userPath);
      switch (expr.op) {
        case '==':
          return literalEquals(l, r);
        case '!=':
          return l !== MISSING && r !== MISSING && !literalEquals(l, r);
        case '<':
        case '<=':
        case '>':
        case '>=': {
          // Ordering is defined for integers only (policy: integer conditions).
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
  }
}
