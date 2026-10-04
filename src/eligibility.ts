export class EligibilityConflictError extends Error {
  constructor(version: number) {
    super(`conflicting weights for eligibility version ${version}`);
    this.name = 'EligibilityConflictError';
  }
}

export class MissingEligibilityVersionError extends Error {
  constructor(version: number) {
    super(`eligibility version ${version} not found`);
    this.name = 'MissingEligibilityVersionError';
  }
}

function weightsEqual(a: ReadonlyMap<string, bigint>, b: ReadonlyMap<string, bigint>): boolean {
  if (a.size !== b.size) return false;
  for (const [voterId, weight] of a) {
    if (b.get(voterId) !== weight) return false;
  }
  return true;
}

export class EligibilityTable {
  readonly #versions = new Map<number, ReadonlyMap<string, bigint>>();

  addVersion(version: number, weights: ReadonlyMap<string, bigint> | Record<string, bigint>): void {
    if (!Number.isSafeInteger(version) || version < 0) {
      throw new TypeError(`eligibility version must be a non-negative safe integer, got ${version}`);
    }
    const entries = weights instanceof Map ? [...weights.entries()] : Object.entries(weights);
    const snapshot = new Map<string, bigint>();
    for (const [voterId, weight] of entries) {
      if (typeof voterId !== 'string' || voterId.length === 0) {
        throw new TypeError('eligibility voter id must be a non-empty string');
      }
      if (typeof weight !== 'bigint' || weight <= 0n) {
        throw new TypeError(`eligibility weight for ${voterId} must be a positive bigint`);
      }
      snapshot.set(voterId, weight);
    }
    const existing = this.#versions.get(version);
    if (existing !== undefined) {
      if (!weightsEqual(existing, snapshot)) {
        throw new EligibilityConflictError(version);
      }
      return;
    }
    this.#versions.set(version, snapshot);
  }

  has(version: number): boolean {
    return this.#versions.has(version);
  }

  getVersion(version: number): ReadonlyMap<string, bigint> | undefined {
    return this.#versions.get(version);
  }

  weightOf(version: number, voterId: string): bigint | undefined {
    return this.#versions.get(version)?.get(voterId);
  }
}
