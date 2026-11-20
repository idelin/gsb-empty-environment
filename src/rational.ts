export interface Rational {
  readonly n: bigint;
  readonly d: bigint;
}

export interface RationalJson {
  readonly numerator: string;
  readonly denominator: string;
}

export type RationalInput =
  | bigint
  | number
  | string
  | Rational
  | RationalJson
  | { readonly num: bigint | number | string; readonly den: bigint | number | string }
  | { readonly n: bigint | number | string; readonly d: bigint | number | string }
  | readonly [bigint | number | string, bigint | number | string];

function absBigInt(value: bigint): bigint {
  return value < 0n ? -value : value;
}

function gcd(a: bigint, b: bigint): bigint {
  let x = absBigInt(a);
  let y = absBigInt(b);
  while (y !== 0n) {
    const next = x % y;
    x = y;
    y = next;
  }
  return x;
}

function decimalToFraction(text: string): Rational {
  const match = /^([+-]?)(\d+)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/.exec(text);
  if (match === null) {
    throw new TypeError(`Invalid rational number: ${text}`);
  }
  const sign = match[1] === "-" ? -1n : 1n;
  const whole = match[2] ?? "0";
  const fraction = match[3] ?? "";
  const exponent = Number(match[4] ?? "0");
  if (!Number.isSafeInteger(exponent) || Math.abs(exponent) > 100_000) {
    throw new RangeError(`Rational exponent is too large: ${text}`);
  }
  let numerator = BigInt(whole + fraction);
  let denominator = 10n ** BigInt(fraction.length);
  if (exponent >= 0) {
    numerator *= 10n ** BigInt(exponent);
  } else {
    denominator *= 10n ** BigInt(-exponent);
  }
  return normalize(numerator * sign, denominator);
}

function field(object: Record<string, unknown>, names: readonly string[]): unknown {
  for (const name of names) {
    if (Object.prototype.hasOwnProperty.call(object, name)) {
      return object[name];
    }
  }
  return undefined;
}

function normalize(n: bigint, d: bigint): Rational {
  if (d === 0n) {
    throw new RangeError("Rational denominator must not be zero");
  }
  if (n === 0n) {
    return Object.freeze({ n: 0n, d: 1n });
  }
  if (d < 0n) {
    n = -n;
    d = -d;
  }
  const divisor = gcd(n, d);
  return Object.freeze({ n: n / divisor, d: d / divisor });
}

export function rational(value: RationalInput): Rational {
  if (typeof value === "bigint") {
    return normalize(value, 1n);
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      throw new TypeError("Rational number must be finite");
    }
    return decimalToFraction(String(value));
  }
  if (typeof value === "string") {
    const trimmed = value.trim();
    if (trimmed === "") {
      throw new TypeError("Rational string must not be empty");
    }
    const slash = trimmed.indexOf("/");
    if (slash >= 0) {
      const numerator = trimmed.slice(0, slash).trim();
      const denominator = trimmed.slice(slash + 1).trim();
      if (!/^[+-]?\d+$/.test(numerator) || !/^[+-]?\d+$/.test(denominator)) {
        throw new TypeError(`Invalid rational string: ${value}`);
      }
      return normalize(BigInt(numerator), BigInt(denominator));
    }
    return decimalToFraction(trimmed);
  }
  if (Array.isArray(value)) {
    if (value.length !== 2) {
      throw new TypeError("Rational tuple must have exactly two entries");
    }
    return div(value[0], value[1]);
  }
  if (typeof value === "object" && value !== null) {
    const object = value as unknown as Record<string, unknown>;
    const numerator = field(object, ["n", "num", "numerator"]);
    const denominator = field(object, ["d", "den", "denominator", "denom"]);
    if (numerator === undefined || denominator === undefined) {
      throw new TypeError("Rational object must contain a numerator and denominator");
    }
    return div(numerator as RationalInput, denominator as RationalInput);
  }
  throw new TypeError(`Unsupported rational value: ${String(value)}`);
}

export function integer(value: RationalInput): bigint {
  const result = rational(value);
  if (result.d !== 1n) {
    throw new TypeError("Expected an integer rational");
  }
  return result.n;
}

export function add(a: RationalInput, b: RationalInput): Rational {
  const x = rational(a);
  const y = rational(b);
  return normalize(x.n * y.d + y.n * x.d, x.d * y.d);
}

export function sub(a: RationalInput, b: RationalInput): Rational {
  const x = rational(a);
  const y = rational(b);
  return normalize(x.n * y.d - y.n * x.d, x.d * y.d);
}

export function mul(a: RationalInput, b: RationalInput): Rational {
  const x = rational(a);
  const y = rational(b);
  return normalize(x.n * y.n, x.d * y.d);
}

export function div(a: RationalInput, b: RationalInput): Rational {
  const x = rational(a);
  const y = rational(b);
  return normalize(x.n * y.d, x.d * y.n);
}

export function neg(a: RationalInput): Rational {
  const x = rational(a);
  return Object.freeze({ n: -x.n, d: x.d });
}

export function abs(a: RationalInput): Rational {
  const x = rational(a);
  return x.n < 0n ? neg(x) : x;
}

export function cmp(a: RationalInput, b: RationalInput): -1 | 0 | 1 {
  const x = rational(a);
  const y = rational(b);
  const left = x.n * y.d;
  const right = y.n * x.d;
  return left < right ? -1 : left > right ? 1 : 0;
}

export function eq(a: RationalInput, b: RationalInput): boolean {
  return cmp(a, b) === 0;
}

export function isZero(a: RationalInput): boolean {
  return rational(a).n === 0n;
}

export function sign(a: RationalInput): -1 | 0 | 1 {
  const x = rational(a).n;
  return x < 0n ? -1 : x > 0n ? 1 : 0;
}

export function min(a: RationalInput, b: RationalInput): Rational {
  return cmp(a, b) <= 0 ? rational(a) : rational(b);
}

export function max(a: RationalInput, b: RationalInput): Rational {
  return cmp(a, b) >= 0 ? rational(a) : rational(b);
}

export function rationalKey(value: RationalInput): string {
  const x = rational(value);
  return `${x.n}/${x.d}`;
}

export function toRationalJson(value: RationalInput): RationalJson {
  const x = rational(value);
  return Object.freeze({ numerator: x.n.toString(), denominator: x.d.toString() });
}

export function fromRationalJson(value: RationalJson): Rational {
  return normalize(BigInt(value.numerator), BigInt(value.denominator));
}

export function rationalToString(value: RationalInput): string {
  const x = rational(value);
  return x.d === 1n ? x.n.toString() : `${x.n.toString()}/${x.d.toString()}`;
}

export const ZERO = normalize(0n, 1n);
export const ONE = normalize(1n, 1n);
