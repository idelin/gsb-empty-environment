/**
 * Storage abstraction.
 *
 * The real backend only guarantees that a single compare-and-swap write is
 * atomic. There is no multi-key transaction: multi-record protocols must be
 * built out of independent CAS writes plus idempotent reconciliation.
 */

/** A versioned storage entry. Version 0 means "key does not exist". */
export interface CasEntry<T> {
  readonly version: number;
  readonly value: T;
}

export interface CasStore {
  /** Read a key; returns version 0 / value undefined when absent. */
  read<T>(key: string): Promise<CasEntry<T | undefined>>;
  /**
   * Atomic compare-and-swap.
   * `expectedVersion === 0` means create-only; otherwise the stored version
   * must match exactly. Throws {@link CasConflict} when the compare fails.
   */
  write<T>(key: string, value: T, expectedVersion: number): Promise<number>;
  /** Delete only when the current version matches; throws {@link CasConflict}. */
  delete(key: string, expectedVersion: number): Promise<void>;
  /** List all keys currently stored (used for audits and recovery scans). */
  listKeys(): Promise<readonly string[]>;
}

/** Raised when a compare-and-swap loses its race. It is NOT a crash. */
export class CasConflict extends Error {
  readonly key: string;
  readonly expectedVersion: number;
  readonly actualVersion: number;

  constructor(key: string, expectedVersion: number, actualVersion: number) {
    super(`CAS conflict on ${key}: expected v${expectedVersion}, found v${actualVersion}`);
    this.name = "CasConflict";
    this.key = key;
    this.expectedVersion = expectedVersion;
    this.actualVersion = actualVersion;
  }
}

/** A simulated process crash / timeout at a write boundary. */
export class SimulatedCrash extends Error {
  readonly key: string;
  /** True when the backend may or may not have applied the write. */
  readonly ambiguous: boolean;

  constructor(key: string, ambiguous: boolean) {
    super(ambiguous ? `ambiguous crash around write to ${key}` : `crash before write to ${key}`);
    this.name = "SimulatedCrash";
    this.key = key;
    this.ambiguous = ambiguous;
  }
}

/** Reference in-memory CAS store with correctly emulated single-write atomicity. */
export class InMemoryCasStore implements CasStore {
  readonly #entries = new Map<string, { version: number; value: unknown }>();

  async read<T>(key: string): Promise<CasEntry<T | undefined>> {
    const entry = this.#entries.get(key);
    if (!entry) {
      return { version: 0, value: undefined };
    }
    return { version: entry.version, value: entry.value as T };
  }

  async write<T>(key: string, value: T, expectedVersion: number): Promise<number> {
    const current = this.#entries.get(key);
    const actualVersion = current ? current.version : 0;
    if (actualVersion !== expectedVersion) {
      throw new CasConflict(key, expectedVersion, actualVersion);
    }
    const nextVersion = actualVersion + 1;
    this.#entries.set(key, { version: nextVersion, value });
    return nextVersion;
  }

  async delete(key: string, expectedVersion: number): Promise<void> {
    const current = this.#entries.get(key);
    const actualVersion = current ? current.version : 0;
    if (actualVersion !== expectedVersion) {
      throw new CasConflict(key, expectedVersion, actualVersion);
    }
    this.#entries.delete(key);
  }

  async listKeys(): Promise<readonly string[]> {
    return [...this.#entries.keys()].sort();
  }
}

/** Predicate used to target an injected fault at a specific write point. */
export interface FaultRule {
  /** Match when the written key contains this substring. */
  readonly keyIncludes?: string;
  /** Match the Nth matching write (1-based); omit to match every occurrence. */
  readonly occurrence?: number;
  /**
   * "before": the write is definitely not applied.
   * "ambiguous": the wrapper cannot tell whether the write applied; tests
   * flip a coin inside the store to model real timeout uncertainty.
   */
  readonly mode: "before" | "ambiguous";
}

/**
 * Wraps any CAS store and crashes at selected write points while preserving
 * the underlying store's real atomicity. For ambiguous faults the wrapped
 * store is still written atomically; the caller simply never learns the
 * result, exactly like a network timeout.
 */
export class FaultInjectingCasStore implements CasStore {
  readonly #seen = new Map<string, number>();
  readonly #inner: CasStore;
  readonly #rules: readonly FaultRule[];

  constructor(
    inner: CasStore,
    rules: readonly FaultRule[],
  ) {
    this.#inner = inner;
    this.#rules = rules;
  }

  read<T>(key: string): Promise<CasEntry<T | undefined>> {
    return this.#inner.read<T>(key);
  }

  listKeys(): Promise<readonly string[]> {
    return this.#inner.listKeys();
  }

  delete(key: string, expectedVersion: number): Promise<void> {
    return this.#applyFault(key, () => this.#inner.delete(key, expectedVersion));
  }

  async write<T>(key: string, value: T, expectedVersion: number): Promise<number> {
    return this.#applyFault(key, () => this.#inner.write(key, value, expectedVersion));
  }

  async #applyFault<T>(key: string, apply: () => Promise<T>): Promise<T> {
    const matching = this.#rules.filter(
      (rule) => rule.keyIncludes === undefined || key.includes(rule.keyIncludes),
    );
    for (const rule of matching) {
      const count = (this.#seen.get(key) ?? 0) + 1;
      this.#seen.set(key, count);
      if (rule.occurrence === undefined || rule.occurrence === count) {
        if (rule.mode === "before") {
          throw new SimulatedCrash(key, false);
        }
        const applied = Math.random() < 0.5;
        if (applied) {
          await apply();
        }
        throw new SimulatedCrash(key, true);
      }
    }
    return apply();
  }
}
