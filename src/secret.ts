const MASK = '********';

/**
 * Wraps a sensitive value. Display and serialization are masked, but the
 * underlying value is retained so equality / change detection still works.
 */
export class SecretValue<T = unknown> {
  readonly #value: T;

  constructor(value: T) {
    this.#value = value;
    Object.freeze(this);
  }

  /** Explicitly access the raw value. */
  reveal(): T {
    return this.#value;
  }

  /** Compare the underlying values without exposing them. */
  equals(other: unknown): boolean {
    if (other instanceof SecretValue) return deepEquals(this.#value, other.#value);
    return deepEquals(this.#value, other);
  }

  toString(): string {
    return MASK;
  }

  toJSON(): string {
    return MASK;
  }

  [Symbol.for('nodejs.util.inspect.custom')](): string {
    return `Secret(${MASK})`;
  }
}

export function isSecret(value: unknown): value is SecretValue {
  return value instanceof SecretValue;
}

/** Masked representation used by toJSON / explain output. */
export function maskValue(value: unknown): unknown {
  if (value instanceof SecretValue) return MASK;
  if (Array.isArray(value)) return value.map(maskValue);
  if (value !== null && typeof value === 'object') {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value)) out[k] = maskValue(v);
    return out;
  }
  return value;
}

/** Deep equality that unwraps SecretValue instances on either side. */
export function deepEquals(a: unknown, b: unknown): boolean {
  if (a instanceof SecretValue) a = a.reveal();
  if (b instanceof SecretValue) b = b.reveal();
  if (a === b) return true;
  if (typeof a !== typeof b || a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((item, i) => deepEquals(item, b[i]));
  }
  if (typeof a === 'object') {
    const ao = a as Record<string, unknown>;
    const bo = b as Record<string, unknown>;
    const keys = Object.keys(ao);
    if (keys.length !== Object.keys(bo).length) return false;
    return keys.every((k) => k in bo && deepEquals(ao[k], bo[k]));
  }
  return false;
}
