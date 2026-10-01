/**
 * Operation budget for interruptible computation.
 *
 * Every loop iteration of the continued-fraction scan and of the exhaustive
 * reference search consumes budget. When the budget runs out a
 * BudgetExhaustedError is thrown internally and the public APIs convert it
 * into a `{ status: "budget-exhausted", ... }` result carrying partial
 * progress, so callers can resume with a larger budget.
 */

export class BudgetExhaustedError extends Error {
  constructor(message = "operation budget exhausted") {
    super(message);
    this.name = "BudgetExhaustedError";
  }
}

export class Budget {
  private remaining: bigint;

  constructor(limit: bigint | number) {
    const l = BigInt(limit);
    if (l < 0n) throw new RangeError("budget limit must be >= 0");
    this.remaining = l;
  }

  consume(cost: bigint | number = 1): void {
    this.remaining -= BigInt(cost);
    if (this.remaining < 0n) throw new BudgetExhaustedError();
  }

  get left(): bigint {
    return this.remaining;
  }
}
