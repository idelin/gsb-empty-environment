import { DecimalError } from './errors.ts';
import { compare, toScientific, type Decimal } from './decimal.ts';
import { parseDecimal, type ParserOptions } from './parser.ts';

export interface Interval {
  /** Lower bound; omitted means -infinity. */
  min?: Decimal | string;
  /** Upper bound; omitted means +infinity. */
  max?: Decimal | string;
  minExclusive?: boolean;
  maxExclusive?: boolean;
}

function asDecimal(v: Decimal | string, opts?: ParserOptions): Decimal {
  return typeof v === 'string' ? parseDecimal(v, opts) : v;
}

/**
 * Interval membership test. Comparison is exponent-aware and never expands
 * huge exponents, so `inRange('1e999999', { max: '2e999999' })` is instant.
 */
export function inRange(
  value: Decimal | string,
  interval: Interval,
  opts?: ParserOptions,
): boolean {
  const d = asDecimal(value, opts);
  if (interval.min !== undefined) {
    const c = compare(d, asDecimal(interval.min, opts));
    if (interval.minExclusive ? c <= 0 : c < 0) return false;
  }
  if (interval.max !== undefined) {
    const c = compare(d, asDecimal(interval.max, opts));
    if (interval.maxExclusive ? c >= 0 : c > 0) return false;
  }
  return true;
}

/** Like `inRange` but throws OUT_OF_RANGE with the offending value and bounds. */
export function assertInRange(
  value: Decimal | string,
  interval: Interval,
  opts?: ParserOptions,
): Decimal {
  const d = asDecimal(value, opts);
  if (!inRange(d, interval, opts)) {
    const lo = interval.min === undefined ? '-inf' : `${interval.minExclusive ? '(' : '['}${asDecimal(interval.min, opts).raw}`;
    const hi = interval.max === undefined ? '+inf' : `${asDecimal(interval.max, opts).raw}${interval.maxExclusive ? ')' : ']'}`;
    throw new DecimalError(
      'OUT_OF_RANGE',
      `value ${d.raw} (${toScientific(d)}) is outside interval ${lo}, ${hi}`,
    );
  }
  return d;
}
