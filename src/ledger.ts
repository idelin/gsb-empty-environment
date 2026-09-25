import { InsufficientQuotaError } from "./errors.ts";

export interface DimensionState {
  readonly capacity: number;
  readonly used: number;
}

export type Requirements = Readonly<Record<string, number>>;

/**
 * Multi-dimensional quota ledger.
 *
 * Deduction is all-or-nothing: every requested dimension is checked first,
 * and only when *all* have enough headroom is any of them debited. A failed
 * acquisition therefore leaves the ledger untouched.
 */
export class QuotaLedger {
  readonly #capacity = new Map<string, number>();
  readonly #used = new Map<string, number>();

  constructor(capacities: Readonly<Record<string, number>>) {
    for (const [name, capacity] of Object.entries(capacities)) {
      if (!Number.isInteger(capacity) || capacity < 0) {
        throw new TypeError(`capacity for "${name}" must be a non-negative integer`);
      }
      this.#capacity.set(name, capacity);
      this.#used.set(name, 0);
    }
  }

  static #validateRequirements(requirements: Requirements): void {
    const entries = Object.entries(requirements);
    if (entries.length === 0) {
      throw new TypeError("requirements must name at least one dimension");
    }
    for (const [name, amount] of entries) {
      if (!Number.isInteger(amount) || amount <= 0) {
        throw new TypeError(`requirement for "${name}" must be a positive integer`);
      }
    }
  }

  /** True when every requested dimension has enough remaining capacity. */
  canSatisfy(requirements: Requirements): boolean {
    QuotaLedger.#validateRequirements(requirements);
    return Object.entries(requirements).every(
      ([name, amount]) => (this.#used.get(name) ?? 0) + amount <= (this.#capacity.get(name) ?? 0),
    );
  }

  /**
   * Atomically debit every requested dimension.
   * @throws {InsufficientQuotaError} when any dimension is short; nothing is debited.
   */
  deduct(requirements: Requirements): void {
    QuotaLedger.#validateRequirements(requirements);
    const shortages: Record<string, { requested: number; available: number }> = {};
    for (const [name, amount] of Object.entries(requirements)) {
      const available = (this.#capacity.get(name) ?? 0) - (this.#used.get(name) ?? 0);
      if (amount > available) shortages[name] = { requested: amount, available };
    }
    if (Object.keys(shortages).length > 0) {
      throw new InsufficientQuotaError(shortages);
    }
    for (const [name, amount] of Object.entries(requirements)) {
      this.#used.set(name, (this.#used.get(name) ?? 0) + amount);
    }
  }

  /** Credit back every dimension of a previous deduction (cancel/fail rollback). */
  release(requirements: Requirements): void {
    QuotaLedger.#validateRequirements(requirements);
    for (const [name, amount] of Object.entries(requirements)) {
      const used = this.#used.get(name) ?? 0;
      if (amount > used) {
        throw new RangeError(`cannot release ${amount} of "${name}"; only ${used} in use`);
      }
    }
    for (const [name, amount] of Object.entries(requirements)) {
      this.#used.set(name, (this.#used.get(name) ?? 0) - amount);
    }
  }

  snapshot(): Readonly<Record<string, DimensionState>> {
    const out: Record<string, DimensionState> = {};
    for (const [name, capacity] of this.#capacity) {
      out[name] = Object.freeze({ capacity, used: this.#used.get(name) ?? 0 });
    }
    return Object.freeze(out);
  }
}
