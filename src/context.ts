import { Rounding, type RoundingMode } from './rounding.ts';

export const Flag = {
  DivisionByZero: 'DivisionByZero',
  InvalidOperation: 'InvalidOperation',
  Inexact: 'Inexact',
  Rounded: 'Rounded',
} as const;

export type Flag = (typeof Flag)[keyof typeof Flag];

export interface ContextOptions {
  precision?: number;
  rounding?: RoundingMode;
}

/**
 * An arithmetic context: precision + rounding mode + a sticky flag set.
 * Contexts are fully independent; flags accumulate until clearFlags().
 */
export class Context {
  precision: number;
  rounding: RoundingMode;
  readonly flags: Set<Flag> = new Set();

  constructor(options: ContextOptions = {}) {
    this.precision = options.precision ?? 28;
    this.rounding = options.rounding ?? Rounding.HALF_EVEN;
    if (!Number.isSafeInteger(this.precision) || this.precision < 1) {
      throw new RangeError(`precision must be a positive integer, got ${this.precision}`);
    }
  }

  clearFlags(): void {
    this.flags.clear();
  }

  hasFlag(flag: Flag): boolean {
    return this.flags.has(flag);
  }
}

export const DEFAULT_CONTEXT = new Context();
