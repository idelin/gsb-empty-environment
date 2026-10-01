import type { Context } from './context.ts';

export const Rounding = {
  HALF_EVEN: 'HALF_EVEN',
  HALF_UP: 'HALF_UP',
  HALF_DOWN: 'HALF_DOWN',
  DOWN: 'DOWN',
  UP: 'UP',
  CEILING: 'CEILING',
  FLOOR: 'FLOOR',
  ROUND_05UP: '05UP',
} as const;

export type RoundingMode = (typeof Rounding)[keyof typeof Rounding];

const pow10Cache: bigint[] = [1n];

export function pow10(n: number): bigint {
  if (!Number.isSafeInteger(n) || n < 0) {
    throw new RangeError(`pow10: invalid exponent ${n}`);
  }
  for (let i = pow10Cache.length; i <= n; i++) {
    pow10Cache[i] = pow10Cache[i - 1] * 10n;
  }
  return pow10Cache[n];
}

export function absBig(n: bigint): bigint {
  return n < 0n ? -n : n;
}

export function digitCount(n: bigint): number {
  return absBig(n).toString().length;
}

export interface RoundedParts {
  coeff: bigint;
  exp: number;
}

function cmpToHalf(discarded: bigint, unit: bigint, sticky: boolean): number {
  const twice = 2n * discarded;
  if (twice > unit) return 1;
  if (twice < unit) return -1;
  return sticky ? 1 : 0;
}

/**
 * Drop `drop` decimal digits from a signed coefficient, rounding the
 * magnitude according to `mode`. `sticky` indicates additional non-zero
 * residue below the dropped digits (used by division remainders).
 * Records Rounded / Inexact flags on the context.
 */
export function roundDrop(
  coeff: bigint,
  exp: number,
  drop: number,
  mode: RoundingMode,
  ctx: Context,
  sticky = false,
): RoundedParts {
  if (drop <= 0) return { coeff, exp };
  const unit = pow10(drop);
  const negative = coeff < 0n;
  const magnitude = absBig(coeff);
  const kept = magnitude / unit;
  const discarded = magnitude % unit;
  const hasResidue = discarded !== 0n || sticky;

  ctx.flags.add('Rounded');
  if (hasResidue) ctx.flags.add('Inexact');

  let increment = false;
  switch (mode) {
    case 'DOWN':
      increment = false;
      break;
    case 'UP':
      increment = hasResidue;
      break;
    case 'CEILING':
      increment = !negative && hasResidue;
      break;
    case 'FLOOR':
      increment = negative && hasResidue;
      break;
    case 'HALF_UP':
      increment = cmpToHalf(discarded, unit, sticky) >= 0;
      break;
    case 'HALF_DOWN':
      increment = cmpToHalf(discarded, unit, sticky) > 0;
      break;
    case 'HALF_EVEN': {
      const cmp = cmpToHalf(discarded, unit, sticky);
      increment = cmp > 0 || (cmp === 0 && (kept & 1n) === 1n);
      break;
    }
    case '05UP': {
      const last = kept % 10n;
      increment = hasResidue && (last === 0n || last === 5n);
      break;
    }
  }

  const rounded = increment ? kept + 1n : kept;
  return { coeff: negative ? -rounded : rounded, exp: exp + drop };
}

/**
 * Round a signed coefficient so it has at most `ctx.precision` significant
 * digits, adjusting the exponent. Handles the carry case (999 -> 100).
 */
export function roundToPrecision(
  coeff: bigint,
  exp: number,
  ctx: Context,
): RoundedParts {
  const digits = digitCount(coeff);
  if (digits <= ctx.precision) return { coeff, exp };
  let parts = roundDrop(coeff, exp, digits - ctx.precision, ctx.rounding, ctx);
  if (digitCount(parts.coeff) > ctx.precision) {
    parts = { coeff: parts.coeff / 10n, exp: parts.exp + 1 };
  }
  return parts;
}
