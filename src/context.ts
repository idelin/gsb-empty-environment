import { RoundingMode } from './rounding.ts';

/**
 * Error / status flags, kept on a Context until explicitly cleared.
 * Division by zero and "result does not fit the precision" are distinct
 * signals: DIVISION_BY_ZERO vs INEXACT.
 */
export const Flag = {
  /** A division by zero was attempted (the operation also throws). */
  DIVISION_BY_ZERO: 'DIVISION_BY_ZERO',
  /** The exact result did not fit the context precision; the returned value is rounded. */
  INEXACT: 'INEXACT',
  /** Digits were discarded (may still be exact if they were all zero). */
  ROUNDED: 'ROUNDED',
  /** An operand or argument was not a valid decimal. */
  INVALID_OPERATION: 'INVALID_OPERATION',
} as const;

export type Flag = (typeof Flag)[keyof typeof Flag];

export interface ContextOptions {
  /** Number of significant decimal digits used by context-aware operations. */
  precision?: number;
  rounding?: RoundingMode;
}

/**
 * A Context carries precision + rounding mode and accumulates sticky flags.
 * Contexts are independent: each one has its own precision and flag set, so
 * concurrent calculations never share state unless you pass the same Context.
 */
export class Context {
  readonly precision: number;
  readonly rounding: RoundingMode;
  readonly flags: Set<Flag> = new Set();

  constructor(options: ContextOptions = {}) {
    const { precision = 28, rounding = RoundingMode.HALF_EVEN } = options;
    if (!Number.isSafeInteger(precision) || precision < 1) {
      throw new RangeError(`precision must be a positive safe integer, got ${precision}`);
    }
    this.precision = precision;
    this.rounding = rounding;
  }

  hasFlag(flag: Flag): boolean {
    return this.flags.has(flag);
  }

  clearFlags(): void {
    this.flags.clear();
  }

  toString(): string {
    return `Context(precision=${this.precision}, rounding=${this.rounding}, flags=[${[...this.flags].join(', ')}])`;
  }
}

/** Shared default context, used when an operation needs one and none is given. */
export const defaultContext = new Context();
