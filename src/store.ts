import { randomUUID } from "node:crypto";

/**
 * The only persistence primitive available to the relay host.
 * A value is written only when the key still holds the expected version.
 * There is no multi-key transaction: every durable operation must be
 * expressible as compare-and-swap on a single key.
 */
export interface CasRecord<T> {
  readonly version: string;
  readonly value: T;
}

export interface CasStore {
  get<T>(key: string): Promise<CasRecord<T> | null>;
  /**
   * Atomically replace a key. Pass `expectedVersion: null` to create a
   * missing key; pass the last seen version to overwrite it.
   * @returns the new record, or null if the compare failed.
   */
  cas<T>(key: string, expectedVersion: string | null, value: T): Promise<CasRecord<T> | null>;
}

/** In-memory reference implementation. The host may back this with a KV store. */
export class InMemoryCasStore implements CasStore {
  readonly #records = new Map<string, CasRecord<unknown>>();

  async get<T>(key: string): Promise<CasRecord<T> | null> {
    const record = this.#records.get(key);
    return record ? (record as CasRecord<T>) : null;
  }

  async cas<T>(key: string, expectedVersion: string | null, value: T): Promise<CasRecord<T> | null> {
    const current = this.#records.get(key);
    if (expectedVersion === null) {
      if (current) {
        return null;
      }
    } else if (!current || current.version !== expectedVersion) {
      return null;
    }
    const next: CasRecord<T> = { version: randomUUID(), value };
    this.#records.set(key, next);
    return next;
  }
}

/** Retry a single-key read/modify/CAS loop until the swap succeeds. */
export async function withCasRetry<T>(
  store: CasStore,
  key: string,
  mutate: (current: T | null) => T,
): Promise<CasRecord<T>> {
  for (;;) {
    const existing = await store.get<T>(key);
    const nextValue = mutate(existing ? existing.value : null);
    const swapped = await store.cas<T>(key, existing ? existing.version : null, nextValue);
    if (swapped) {
      return swapped;
    }
  }
}
