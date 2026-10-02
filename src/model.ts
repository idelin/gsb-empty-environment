import { Fraction } from './fraction.ts';

export interface ItemInput {
  id: string;
  /** Rational weight: Fraction | bigint | string ("3/7", "1.5", "2"). Negative is rejected. */
  weight: Fraction | bigint | string;
  /** Integer lower bound for this item (default 0). */
  lower?: bigint;
  /** Integer upper bound for this item (default: total). */
  upper?: bigint;
}

export interface GroupInput {
  id: string;
  /** Member item ids; groups may overlap arbitrarily. */
  members: string[];
  /** Integer lower bound on the sum of members. */
  lower?: bigint;
  /** Integer upper bound on the sum of members. */
  upper?: bigint;
}

export interface SolveInput {
  /** Total integer amount to distribute (>= 0). */
  total: bigint;
  items: ItemInput[];
  groups?: GroupInput[];
  /**
   * Previous allocation (re-weighting scenario). When present the solver
   * lexicographically minimizes (1) total movement sum|x_i - prev_i|,
   * then (2) total deviation from the new ideal shares.
   */
  previous?: Record<string, bigint>;
  /** Safety cap on enumerated nodes (exact small-scale solver). */
  maxNodes?: number;
}

export interface NormalizedItem {
  id: string;
  weight: Fraction;
  lower: bigint;
  upper: bigint;
}

export interface NormalizedGroup {
  id: string;
  memberIdx: number[];
  lower: bigint | null;
  upper: bigint | null;
}

export interface NormalizedProblem {
  total: bigint;
  items: NormalizedItem[];
  groups: NormalizedGroup[];
  previous: bigint[] | null;
  /** Ideal rational shares q_i = total * w_i / W (exact). */
  ideal: Fraction[];
  maxNodes: number;
}

export type ValidationError = { ok: false; error: string };
export type ValidationOk = { ok: true; problem: NormalizedProblem };
export type Validation = ValidationError | ValidationOk;

export const DEFAULT_MAX_NODES = 2_000_000;

/**
 * Normalize and validate raw input. Rejects: negative total, negative
 * weights, all-zero weights, duplicate ids, unknown group members,
 * inverted bounds, unknown ids in `previous`.
 */
export function normalize(input: SolveInput): Validation {
  const { total } = input;
  if (typeof total !== 'bigint') return { ok: false, error: 'total must be a bigint' };
  if (total < 0n) return { ok: false, error: `total must be >= 0, got ${total}` };
  if (!Array.isArray(input.items) || input.items.length === 0) {
    return { ok: false, error: 'items must be a non-empty array' };
  }

  const seen = new Set<string>();
  const items: NormalizedItem[] = [];
  for (const raw of input.items) {
    if (typeof raw.id !== 'string' || raw.id === '') {
      return { ok: false, error: 'every item needs a non-empty string id' };
    }
    if (seen.has(raw.id)) return { ok: false, error: `duplicate item id "${raw.id}"` };
    seen.add(raw.id);
    let weight: Fraction;
    try {
      weight = Fraction.from(raw.weight);
    } catch (e) {
      return { ok: false, error: `item "${raw.id}": ${(e as Error).message}` };
    }
    if (weight.isNegative()) {
      return { ok: false, error: `item "${raw.id}": negative weight ${weight} rejected` };
    }
    const lower = raw.lower ?? 0n;
    const upper = raw.upper ?? total;
    if (lower > upper) {
      return { ok: false, error: `item "${raw.id}": lower ${lower} > upper ${upper}` };
    }
    items.push({ id: raw.id, weight, lower, upper });
  }

  const weightSum = items.reduce((acc, it) => acc.add(it.weight), Fraction.zero());
  if (weightSum.isZero()) return { ok: false, error: 'all weights are zero' };

  const indexOf = new Map(items.map((it, i) => [it.id, i]));
  const groups: NormalizedGroup[] = [];
  for (const g of input.groups ?? []) {
    const memberIdx: number[] = [];
    for (const m of g.members) {
      const idx = indexOf.get(m);
      if (idx === undefined) {
        return { ok: false, error: `group "${g.id}": unknown member "${m}"` };
      }
      memberIdx.push(idx);
    }
    const lower = g.lower ?? null;
    const upper = g.upper ?? null;
    if (lower !== null && upper !== null && lower > upper) {
      return { ok: false, error: `group "${g.id}": lower ${lower} > upper ${upper}` };
    }
    groups.push({ id: g.id, memberIdx, lower, upper });
  }

  let previous: bigint[] | null = null;
  if (input.previous !== undefined) {
    previous = items.map(() => 0n);
    for (const [id, v] of Object.entries(input.previous)) {
      const idx = indexOf.get(id);
      if (idx === undefined) {
        return { ok: false, error: `previous allocation references unknown id "${id}"` };
      }
      if (typeof v !== 'bigint') {
        return { ok: false, error: `previous["${id}"] must be a bigint` };
      }
      previous[idx] = v;
    }
  }

  const ideal = items.map((it) =>
    new Fraction(total, 1n).mul(it.weight).div(weightSum),
  );

  return {
    ok: true,
    problem: {
      total,
      items,
      groups,
      previous,
      ideal,
      maxNodes: input.maxNodes ?? DEFAULT_MAX_NODES,
    },
  };
}
