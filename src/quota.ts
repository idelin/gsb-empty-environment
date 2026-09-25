import { InvalidRequestError, type Shortage } from './errors.ts';

export interface DimensionState {
  readonly limit: number;
  readonly used: number;
}

export interface DeductionLeg {
  readonly account: string;
  readonly dimensions: Readonly<Record<string, number>>;
}

interface MutableDimension {
  limit: number;
  used: number;
}

function assertAmount(label: string, value: number, allowZero: boolean): void {
  if (!Number.isSafeInteger(value) || value < 0 || (!allowZero && value === 0)) {
    throw new InvalidRequestError(`${label} must be a ${allowZero ? 'non-negative' : 'positive'} safe integer`);
  }
}

export class QuotaStore {
  readonly #accounts = new Map<string, Map<string, MutableDimension>>();

  defineAccount(accountId: string, dimensions: Readonly<Record<string, number>>): void {
    if (typeof accountId !== 'string' || accountId.length === 0) {
      throw new InvalidRequestError('account id must be a non-empty string');
    }
    if (this.#accounts.has(accountId)) {
      throw new InvalidRequestError(`account "${accountId}" already defined`);
    }
    const dims = new Map<string, MutableDimension>();
    for (const [name, limit] of Object.entries(dimensions)) {
      assertAmount(`limit of ${accountId}/${name}`, limit, true);
      dims.set(name, { limit, used: 0 });
    }
    this.#accounts.set(accountId, dims);
  }

  #dimension(account: string, dimension: string): MutableDimension {
    const dims = this.#accounts.get(account);
    if (!dims) throw new InvalidRequestError(`unknown account "${account}"`);
    const dim = dims.get(dimension);
    if (!dim) throw new InvalidRequestError(`unknown dimension "${dimension}" on account "${account}"`);
    return dim;
  }

  available(account: string, dimension: string): number {
    const dim = this.#dimension(account, dimension);
    return dim.limit - dim.used;
  }

  checkAll(legs: readonly DeductionLeg[]): Shortage[] {
    const shortages: Shortage[] = [];
    for (const leg of legs) {
      for (const [dimension, amount] of Object.entries(leg.dimensions)) {
        const available = this.available(leg.account, dimension);
        if (available < amount) {
          shortages.push({ account: leg.account, dimension, requested: amount, available });
        }
      }
    }
    return shortages;
  }

  deductAll(legs: readonly DeductionLeg[]): void {
    for (const leg of legs) {
      for (const [dimension, amount] of Object.entries(leg.dimensions)) {
        this.#dimension(leg.account, dimension).used += amount;
      }
    }
  }

  restoreAll(legs: readonly DeductionLeg[]): void {
    for (const leg of legs) {
      for (const [dimension, amount] of Object.entries(leg.dimensions)) {
        const dim = this.#dimension(leg.account, dimension);
        dim.used -= amount;
        if (dim.used < 0) {
          throw new InvalidRequestError(`restore would drive ${leg.account}/${dimension} below zero`);
        }
      }
    }
  }

  snapshot(account: string): Record<string, DimensionState> {
    const dims = this.#accounts.get(account);
    if (!dims) throw new InvalidRequestError(`unknown account "${account}"`);
    const out: Record<string, DimensionState> = {};
    for (const [name, dim] of dims) {
      out[name] = Object.freeze({ limit: dim.limit, used: dim.used });
    }
    return out;
  }
}

export function normalizeLegs(legs: readonly DeductionLeg[]): DeductionLeg[] {
  if (!Array.isArray(legs) || legs.length === 0) {
    throw new InvalidRequestError('legs must be a non-empty array');
  }
  const merged = new Map<string, Map<string, number>>();
  for (const leg of legs) {
    if (typeof leg?.account !== 'string' || leg.account.length === 0) {
      throw new InvalidRequestError('leg.account must be a non-empty string');
    }
    if (leg.dimensions === null || typeof leg.dimensions !== 'object') {
      throw new InvalidRequestError('leg.dimensions must be an object');
    }
    let dims = merged.get(leg.account);
    if (!dims) {
      dims = new Map();
      merged.set(leg.account, dims);
    }
    for (const [dimension, amount] of Object.entries(leg.dimensions)) {
      assertAmount(`amount of ${leg.account}/${dimension}`, amount, false);
      dims.set(dimension, (dims.get(dimension) ?? 0) + amount);
    }
  }
  return [...merged.entries()].map(([account, dims]) =>
    Object.freeze({
      account,
      dimensions: Object.freeze(Object.fromEntries(dims)),
    }),
  );
}
