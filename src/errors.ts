/**
 * Thrown when an old key and its target new key are both present but hold
 * different values. The engine refuses to guess which one is right.
 */
export class MigrationConflictError extends Error {
  readonly ruleId: string;
  readonly key: string;
  /** Value already present under the new key. */
  readonly existing: unknown;
  /** Value the forward transform derived from the old key(s). */
  readonly incoming: unknown;

  constructor(ruleId: string, key: string, existing: unknown, incoming: unknown) {
    super(
      `Migration conflict in rule "${ruleId}" for key "${key}": ` +
        `the new key already exists with a different value; refusing to guess.`,
    );
    this.name = 'MigrationConflictError';
    this.ruleId = ruleId;
    this.key = key;
    this.existing = existing;
    this.incoming = incoming;
  }
}
