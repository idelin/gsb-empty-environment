import { QuantityTypeError } from './errors.ts';
import { Rational } from './rational.ts';
import { convertAbsoluteValue, convertDeltaValue, type Unit } from './unit.ts';

export type QuantityInput = bigint | number | string | Rational;

function toRational(value: QuantityInput): Rational {
  return Rational.from(value);
}

/**
 * A difference between two absolutes: durations, temperature differences,
 * lengths. Offsets never apply to deltas (Δ1 °C === Δ1 K exactly).
 */
export class DeltaQuantity {
  readonly kind = 'delta' as const;
  readonly value: Rational;
  readonly unit: Unit;

  private constructor(value: Rational, unit: Unit) {
    this.value = value;
    this.unit = unit;
  }

  static of(value: QuantityInput, unit: Unit): DeltaQuantity {
    return new DeltaQuantity(toRational(value), unit);
  }

  to(unit: Unit): DeltaQuantity {
    return new DeltaQuantity(convertDeltaValue(this.value, this.unit, unit), unit);
  }

  add(other: DeltaQuantity): DeltaQuantity {
    return new DeltaQuantity(this.value.add(other.to(this.unit).value), this.unit);
  }

  sub(other: DeltaQuantity): DeltaQuantity {
    return new DeltaQuantity(this.value.sub(other.to(this.unit).value), this.unit);
  }

  neg(): DeltaQuantity {
    return new DeltaQuantity(this.value.neg(), this.unit);
  }

  scaleBy(factor: QuantityInput): DeltaQuantity {
    return new DeltaQuantity(this.value.mul(toRational(factor)), this.unit);
  }

  toString(): string {
    return `Δ${this.value.toString()} ${this.unit.symbol}`;
  }
}

/**
 * A position on an absolute scale: timestamps, absolute temperatures,
 * positions. Adding two absolutes is meaningless and rejected; absolute
 * minus absolute yields a delta.
 */
export class AbsoluteQuantity {
  readonly kind = 'absolute' as const;
  readonly value: Rational;
  readonly unit: Unit;

  private constructor(value: Rational, unit: Unit) {
    this.value = value;
    this.unit = unit;
  }

  static of(value: QuantityInput, unit: Unit): AbsoluteQuantity {
    return new AbsoluteQuantity(toRational(value), unit);
  }

  to(unit: Unit): AbsoluteQuantity {
    return new AbsoluteQuantity(convertAbsoluteValue(this.value, this.unit, unit), unit);
  }

  add(delta: DeltaQuantity): AbsoluteQuantity {
    return new AbsoluteQuantity(this.value.add(delta.to(this.unit).value), this.unit);
  }

  sub(other: AbsoluteQuantity): DeltaQuantity;
  sub(delta: DeltaQuantity): AbsoluteQuantity;
  sub(other: AbsoluteQuantity | DeltaQuantity): AbsoluteQuantity | DeltaQuantity {
    if (other instanceof AbsoluteQuantity) {
      return new DeltaQuantity(this.value.sub(other.to(this.unit).value), this.unit);
    }
    if (other instanceof DeltaQuantity) {
      return new AbsoluteQuantity(this.value.sub(other.to(this.unit).value), this.unit);
    }
    throw new QuantityTypeError('can only subtract a quantity from an absolute quantity');
  }

  toString(): string {
    return `${this.value.toString()} ${this.unit.symbol}`;
  }
}

/** Adding two absolutes is a type error made explicit at runtime too. */
export function addQuantities(
  a: AbsoluteQuantity | DeltaQuantity,
  b: AbsoluteQuantity | DeltaQuantity,
): AbsoluteQuantity | DeltaQuantity {
  if (a instanceof AbsoluteQuantity && b instanceof AbsoluteQuantity) {
    throw new QuantityTypeError(
      `cannot add two absolute quantities (${a.unit.symbol} + ${b.unit.symbol}); subtract them to get a delta`,
    );
  }
  if (a instanceof AbsoluteQuantity) return a.add(b as DeltaQuantity);
  if (b instanceof AbsoluteQuantity) return b.add(a as DeltaQuantity);
  return (a as DeltaQuantity).add(b as DeltaQuantity);
}
