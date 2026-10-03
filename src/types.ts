import type { Rational } from './rational.ts';

/** Weight input: exact Rational, exact decimal/fraction string, or BigInt. Never Number. */
export type WeightInput = Rational | string | bigint;

/** Integer input for totals and bounds: BigInt or decimal string. Never Number. */
export type IntInput = bigint | string;

export interface ItemInput {
  readonly id: string;
  readonly weight: WeightInput;
  /** Per-object lower bound (default 0). */
  readonly lower?: IntInput;
  /** Per-object upper bound (default: total). */
  readonly upper?: IntInput;
}

export interface GroupInput {
  readonly id: string;
  readonly members: readonly string[];
  /** Lower bound on the sum of member allocations (default 0). */
  readonly min?: IntInput;
  /** Upper bound on the sum of member allocations (default: total). */
  readonly max?: IntInput;
}

export interface ApportionProblem {
  readonly total: IntInput;
  readonly items: readonly ItemInput[];
  /** Groups may overlap arbitrarily; all constraints are enforced jointly. */
  readonly groups?: readonly GroupInput[];
}

export type Allocation = Record<string, bigint>;
