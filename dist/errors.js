// Generated from src/errors.ts (regenerate with `npm run build`).
/**
 * Thrown when an old key and its target new key are both present but hold
 * different values. The engine refuses to guess which one is right.
 */
export class MigrationConflictError extends Error {
  constructor(ruleId, key, existing, incoming) {
    super(
      `Migration conflict in rule "${ruleId}" for key "${key}": ` +
        `the new key already exists with a different value; refusing to guess.`,
    );
    this.name = 'MigrationConflictError';
    this.ruleId = ruleId;
    this.key = key;
    /** Value already present under the new key. */
    this.existing = existing;
    /** Value the forward transform derived from the old key(s). */
    this.incoming = incoming;
  }
}
