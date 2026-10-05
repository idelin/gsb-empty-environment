import { BudgetExceededError, type BudgetKind } from "./errors.ts";

export interface BudgetLimits {
  /** tokens/characters consumed while parsing the query */
  parse?: number;
  /** tree nodes expanded while evaluating */
  expand?: number;
  /** predicate comparisons performed */
  compare?: number;
}

const INF = Number.POSITIVE_INFINITY;

export class Budget {
  private readonly limits: Record<BudgetKind, number>;
  private readonly remaining: Record<BudgetKind, number>;

  constructor(limits: BudgetLimits = {}) {
    this.limits = {
      parse: limits.parse ?? INF,
      expand: limits.expand ?? INF,
      compare: limits.compare ?? INF,
    };
    this.remaining = { ...this.limits };
  }

  charge(kind: BudgetKind, n = 1): void {
    this.remaining[kind] -= n;
    if (this.remaining[kind] < 0) {
      throw new BudgetExceededError(kind, this.limits[kind]);
    }
  }

  left(kind: BudgetKind): number {
    return this.remaining[kind];
  }

  static unlimited(): Budget {
    return new Budget();
  }
}
