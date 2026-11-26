export type Scalar = number;
export type Matrix = Scalar[][];

export type GenerationId = string | number;
export type DomainId = string;

export interface RingDescriptor {
  p?: number;
  e?: number;
  modulus?: number;
  q?: number;
}

export function isPrime(value: number): boolean {
  if (!Number.isInteger(value) || value < 2) return false;
  if (value % 2 === 0) return value === 2;
  for (let divisor = 3; divisor * divisor <= value; divisor += 2) {
    if (value % divisor === 0) return false;
  }
  return true;
}

export function primePowerOf(modulus: number): { p: number; e: number; q: number } {
  if (!Number.isInteger(modulus) || modulus < 2) {
    throw new RangeError(`modulus must be an integer >= 2, received ${modulus}`);
  }
  for (let p = 2; p * p <= modulus; p += p === 2 ? 1 : 2) {
    if (modulus % p !== 0) continue;
    let remaining = modulus;
    let e = 0;
    while (remaining % p === 0) {
      remaining /= p;
      e += 1;
    }
    if (remaining !== 1) {
      throw new RangeError(`modulus ${modulus} is not a prime power`);
    }
    if (!isPrime(p)) {
      throw new RangeError(`modulus ${modulus} has non-prime base ${p}`);
    }
    return { p, e, q: modulus };
  }
  return { p: modulus, e: 1, q: modulus };
}

export function normalizeRingDescriptor(descriptor: RingDescriptor | ModRing): {
  p: number;
  e: number;
  q: number;
} {
  if (descriptor instanceof ModRing) {
    return { p: descriptor.p, e: descriptor.e, q: descriptor.q };
  }
  if (descriptor.modulus !== undefined || descriptor.q !== undefined) {
    const modulus = descriptor.modulus ?? descriptor.q!;
    const found = primePowerOf(modulus);
    if (descriptor.p !== undefined && descriptor.p !== found.p) {
      throw new RangeError(`p=${descriptor.p} does not match modulus ${modulus}`);
    }
    if (descriptor.e !== undefined && descriptor.e !== found.e) {
      throw new RangeError(`e=${descriptor.e} does not match modulus ${modulus}`);
    }
    return found;
  }
  const p = descriptor.p;
  const e = descriptor.e ?? 1;
  if (p === undefined || !isPrime(p)) {
    throw new RangeError(`p must be prime, received ${String(p)}`);
  }
  if (!Number.isInteger(e) || e < 1) {
    throw new RangeError(`e must be a positive integer, received ${e}`);
  }
  return { p, e, q: p ** e };
}

export class ModRing {
  readonly p: number;
  readonly e: number;
  readonly q: number;

  constructor(descriptor: number | RingDescriptor = {}) {
    const normalized =
      typeof descriptor === 'number'
        ? primePowerOf(descriptor)
        : normalizeRingDescriptor(descriptor);
    this.p = normalized.p;
    this.e = normalized.e;
    this.q = normalized.q;
  }

  norm(value: number): number {
    if (!Number.isFinite(value)) {
      throw new RangeError(`ring scalar must be finite, received ${value}`);
    }
    const result = Math.trunc(value) % this.q;
    return result < 0 ? result + this.q : result;
  }

  normalize(vector: readonly number[]): number[] {
    return vector.map((value) => this.norm(value));
  }

  add(left: number, right: number): number {
    return this.norm(left + right);
  }

  sub(left: number, right: number): number {
    return this.norm(left - right);
  }

  mul(left: number, right: number): number {
    return this.norm(Math.imul(Math.trunc(left), Math.trunc(right)));
  }

  dot(left: readonly number[], right: readonly number[]): number {
    if (left.length !== right.length) {
      throw new RangeError(`dot product length mismatch: ${left.length} vs ${right.length}`);
    }
    let sum = 0;
    for (let i = 0; i < left.length; i += 1) {
      sum = this.norm(sum + this.mul(left[i], right[i]));
    }
    return sum;
  }

  pow(base: number, exponent: number): number {
    if (!Number.isInteger(exponent) || exponent < 0) {
      throw new RangeError(`exponent must be a non-negative integer`);
    }
    let result = 1;
    let current = this.norm(base);
    let remaining = exponent;
    while (remaining > 0) {
      if (remaining & 1) result = this.mul(result, current);
      current = this.mul(current, current);
      remaining >>= 1;
    }
    return result;
  }

  gcdWithModulus(value: number): number {
    let a = this.norm(value);
    let b = this.q;
    while (b !== 0) {
      const remainder = a % b;
      a = b;
      b = remainder;
    }
    return a;
  }

  valuation(value: number): number {
    const normalized = this.norm(value);
    if (normalized === 0) return this.e;
    let valuePart = normalized;
    let exponent = 0;
    while (valuePart % this.p === 0 && exponent < this.e) {
      valuePart /= this.p;
      exponent += 1;
    }
    return exponent;
  }

  isUnit(value: number): boolean {
    return this.gcdWithModulus(value) === 1;
  }

  inverseUnit(value: number): number {
    const normalized = this.norm(value);
    if (!this.isUnit(normalized)) {
      throw new RangeError(`${normalized} is not a unit in Z/${this.q}Z`);
    }
    return extendedInverse(normalized, this.q);
  }

  solveMultiplier(coefficient: number, target: number): number | null {
    const a = this.norm(coefficient);
    const b = this.norm(target);
    const divisor = this.gcdWithModulus(a);
    if (b % divisor !== 0) return null;
    if (divisor === this.q) return 0;
    const reducedModulus = this.q / divisor;
    if (reducedModulus === 1) return 0;
    const reducedA = (a / divisor) % reducedModulus;
    const reducedB = b / divisor;
    const inverse = extendedInverse(reducedA, reducedModulus);
    return ((reducedB % reducedModulus) * inverse) % reducedModulus;
  }

  elements(): number[] {
    return Array.from({ length: this.q }, (_, index) => index);
  }
}

export function extendedInverse(value: number, modulus: number): number {
  let oldRemainder = value;
  let remainder = modulus;
  let oldCoefficient = 1;
  let coefficient = 0;
  while (remainder !== 0) {
    const quotient = Math.floor(oldRemainder / remainder);
    [oldRemainder, remainder] = [remainder, oldRemainder - quotient * remainder];
    [oldCoefficient, coefficient] = [coefficient, oldCoefficient - quotient * coefficient];
  }
  if (oldRemainder !== 1) {
    throw new RangeError(`${value} has no inverse modulo ${modulus}`);
  }
  const result = oldCoefficient % modulus;
  return result < 0 ? result + modulus : result;
}

export function powerOfPrime(exponent: number, p: number): bigint {
  return BigInt(p) ** BigInt(exponent);
}
