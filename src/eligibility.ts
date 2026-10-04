export class EligibilityVersionNotFoundError extends Error {
  readonly version: number;

  constructor(version: number) {
    super(`eligibility version ${version} has not been published`);
    this.name = 'EligibilityVersionNotFoundError';
    this.version = version;
  }
}

export class EligibilityConflictError extends Error {
  readonly version: number;

  constructor(version: number) {
    super(`eligibility version ${version} already exists with different content`);
    this.name = 'EligibilityConflictError';
    this.version = version;
  }
}

/**
 * Append-only, versioned table of voter weights. Issues pin a specific
 * version, so later weight adjustments never rewrite historical results.
 */
export class EligibilityTable {
  private readonly versions = new Map<number, ReadonlyMap<string, bigint>>();

  publishVersion(
    version: number,
    entries: Iterable<readonly [voterId: string, weight: bigint]>,
  ): void {
    if (!Number.isSafeInteger(version) || version < 0) {
      throw new RangeError(`invalid eligibility version: ${version}`);
    }
    const snapshot = new Map<string, bigint>();
    for (const [voterId, weight] of entries) {
      if (weight < 0n) {
        throw new RangeError(`negative weight for voter ${voterId}`);
      }
      snapshot.set(voterId, weight);
    }
    const existing = this.versions.get(version);
    if (existing !== undefined) {
      if (!sameSnapshot(existing, snapshot)) {
        throw new EligibilityConflictError(version);
      }
      return; // identical re-publish is a no-op (idempotent import)
    }
    this.versions.set(version, snapshot);
  }

  hasVersion(version: number): boolean {
    return this.versions.has(version);
  }

  listVersions(): number[] {
    return [...this.versions.keys()].sort((a, b) => a - b);
  }

  snapshot(version: number): ReadonlyMap<string, bigint> {
    const snapshot = this.versions.get(version);
    if (snapshot === undefined) {
      throw new EligibilityVersionNotFoundError(version);
    }
    return snapshot;
  }

  weightAt(version: number, voterId: string): bigint | undefined {
    return this.snapshot(version).get(voterId);
  }
}

function sameSnapshot(
  a: ReadonlyMap<string, bigint>,
  b: ReadonlyMap<string, bigint>,
): boolean {
  if (a.size !== b.size) return false;
  for (const [voterId, weight] of a) {
    if (b.get(voterId) !== weight) return false;
  }
  return true;
}
