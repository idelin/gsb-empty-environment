import { BudgetExceededError } from './errors.ts';
import type { BudgetPhase } from './errors.ts';
import type { Path } from './path.ts';

/** 查询预算：分别覆盖解析、节点展开、谓词比较三个阶段。 */
export interface QueryBudget {
  parse: number;
  expand: number;
  compare: number;
}

export const UNLIMITED_BUDGET: QueryBudget = {
  parse: Number.MAX_SAFE_INTEGER,
  expand: Number.MAX_SAFE_INTEGER,
  compare: Number.MAX_SAFE_INTEGER,
};

export class BudgetTracker {
  readonly limits: QueryBudget;
  readonly used: QueryBudget = { parse: 0, expand: 0, compare: 0 };
  constructor(limits: Partial<QueryBudget> = {}) {
    this.limits = { ...UNLIMITED_BUDGET, ...limits };
  }
  consume(phase: BudgetPhase, n = 1, path?: Path): void {
    this.used[phase] += n;
    if (this.used[phase] > this.limits[phase]) throw new BudgetExceededError(phase, path);
  }
  get exhausted(): boolean {
    return (
      this.used.parse > this.limits.parse ||
      this.used.expand > this.limits.expand ||
      this.used.compare > this.limits.compare
    );
  }
}
