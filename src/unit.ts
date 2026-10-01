import { dimsEqual, dimsToString, type DimVector } from './dimension.ts';
import { IncommensurableError } from './errors.ts';
import { Rational } from './rational.ts';

/**
 * A unit = dimension vector + exact rational scale relative to the canonical
 * base of that dimension + an optional exact offset (affine units such as
 * degrees Celsius).
 *
 * Canonical mapping for an ABSOLUTE value v in this unit:
 *     base = v * scale + offset
 * For a DELTA value the offset never applies:
 *     base = v * scale
 */
export class Unit {
  readonly name: string;
  readonly symbol: string;
  readonly dims: DimVector;
  readonly scale: Rational;
  readonly offset: Rational;

  constructor(
    name: string,
    symbol: string,
    dims: DimVector,
    scale: Rational,
    offset?: Rational,
  ) {
    if (scale.isZero()) throw new RangeError(`unit ${symbol} has zero scale`);
    this.name = name;
    this.symbol = symbol;
    this.dims = dims;
    this.scale = scale;
    this.offset = offset ?? Rational.ZERO;
  }

  get isAffine(): boolean {
    return !this.offset.isZero();
  }

  toString(): string {
    return `${this.symbol} [${dimsToString(this.dims)}]`;
  }
}

export function assertCommensurable(from: Unit, to: Unit): void {
  if (!dimsEqual(from.dims, to.dims)) {
    throw new IncommensurableError(
      `cannot convert ${from.symbol} [${dimsToString(from.dims)}] to ${to.symbol} [${dimsToString(to.dims)}]: incompatible dimensions`,
    );
  }
}

/** Delta conversion: scale ratio only, offsets are never applied. */
export function convertDeltaValue(value: Rational, from: Unit, to: Unit): Rational {
  assertCommensurable(from, to);
  return value.mul(from.scale).div(to.scale);
}

/** Absolute conversion: full affine map through the canonical base. */
export function convertAbsoluteValue(value: Rational, from: Unit, to: Unit): Rational {
  assertCommensurable(from, to);
  const base = value.mul(from.scale).add(from.offset);
  return base.sub(to.offset).div(to.scale);
}
