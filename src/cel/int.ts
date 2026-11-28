/**
 * CEL 64-bit signed integer semantics. JavaScript numbers cannot represent
 * all int64 values, so integer literals and integer arithmetic are boxed in
 * this class. Integer division truncates toward zero, matching CEL.
 */
export class CelInt {
  readonly value: bigint;

  constructor(value: bigint | number | string) {
    if (typeof value === 'number') {
      if (!Number.isFinite(value) || !Number.isInteger(value)) {
        throw new TypeError(`CelInt requires an integer, got: ${value}`);
      }
      this.value = BigInt(value);
    } else if (typeof value === 'string') {
      this.value = BigInt(value);
    } else {
      this.value = value;
    }
  }

 static of(value: bigint | number | string): CelInt {
    return new CelInt(value);
  }

  isZero(): boolean {
    return this.value === 0n;
  }

  equals(other: unknown): boolean {
    return other instanceof CelInt && other.value === this.value;
  }

  toString(): string {
    return this.value.toString();
  }
}

const INT64_MIN = -9223372036854775808n;
const INT64_MAX = 9223372036854775807n;

export function int64Checked(v: bigint): bigint {
  if (v < INT64_MIN || v > INT64_MAX) {
    throw new RangeError(`integer overflow: ${v}`);
  }
  return v;
}

/** Truncated division (toward zero), CEL semantics. */
export function truncDiv(a: bigint, b: bigint): bigint {
  if (b === 0n) {
    throw new CelDivisionByZero('division by zero');
  }
  // BigInt division truncates the fractional part, which is truncation toward zero.
  return a / b;
}

export class CelDivisionByZero extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CelDivisionByZero';
  }
}
