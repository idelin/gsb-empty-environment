import type { Event, ReplaceabilityClass } from './types.ts';

/**
 * NIP-01/NIP-16 replaceability classification by kind:
 * - 0, 3 and 10000..19999 are replaceable (one latest version per pubkey+kind);
 * - 20000..29999 are ephemeral (not persisted, no replacement arbitration here);
 * - 30000..39999 are parameterized replaceable (keyed additionally by the `d` tag);
 * - everything else is a regular append-only event.
 */
export function getReplaceabilityClass(kind: number): ReplaceabilityClass {
  if (kind === 0 || kind === 3 || (kind >= 10000 && kind <= 19999)) {
    return 'replaceable';
  }
  if (kind >= 20000 && kind <= 29999) {
    return 'ephemeral';
  }
  if (kind >= 30000 && kind <= 39999) {
    return 'parameterized';
  }
  return 'regular';
}

/**
 * Returns the value of the first `d` tag. A missing tag — or a `d` tag with
 * no second element — is the empty string, matching NIP-01's rule that
 * parameterized events without a `d` value share the empty-string key.
 */
export function getDTagValue(event: Pick<Event, 'tags'>): string {
  for (const tag of event.tags) {
    if (tag[0] === 'd') {
      return tag[1] ?? '';
    }
  }
  return '';
}

/**
 * Storage key of a replaceable/parameterized event, or null for events that
 * are never deduplicated (regular and ephemeral).
 */
export function getStorageKey(event: Pick<Event, 'pubkey' | 'kind' | 'tags'>): string | null {
  const klass = getReplaceabilityClass(event.kind);
  if (klass === 'replaceable') {
    return `r:${event.pubkey}:${event.kind}`;
  }
  if (klass === 'parameterized') {
    return `p:${event.pubkey}:${event.kind}:${getDTagValue(event)}`;
  }
  return null;
}

/**
 * Replacement arbitration: the event with the larger `created_at` wins; on an
 * exact tie, the event whose id is lexicographically smaller wins. This makes
 * the outcome deterministic and independent of arrival order, so replaying an
 * old event can never overwrite newer state.
 */
export function shouldReplace(
  incumbent: Pick<Event, 'id' | 'created_at'>,
  challenger: Pick<Event, 'id' | 'created_at'>,
): boolean {
  if (challenger.created_at > incumbent.created_at) {
    return true;
  }
  if (challenger.created_at === incumbent.created_at) {
    return challenger.id < incumbent.id;
  }
  return false;
}

export type IngestAction =
  | 'store'
  | 'replace'
  | 'duplicate'
  | 'reject_stale'
  | 'ephemeral';

export interface IngestDecision {
  action: IngestAction;
  class: ReplaceabilityClass;
  /** Deduplication key for replaceable/parameterized events, otherwise null. */
  key: string | null;
  event: Event;
  /** The currently stored event for the key when one exists, otherwise null. */
  incumbent: Event | null;
}

/**
 * In-memory index of the latest replaceable/parameterized event per key.
 * Feed it only events that already passed `validateEvent`.
 */
export class ReplaceableEventIndex {
  readonly #byKey = new Map<string, Event>();

  get size(): number {
    return this.#byKey.size;
  }

  get(key: string): Event | undefined {
    return this.#byKey.get(key);
  }

  has(key: string): boolean {
    return this.#byKey.has(key);
  }

  delete(key: string): boolean {
    return this.#byKey.delete(key);
  }

  ingest(event: Event): IngestDecision {
    const klass = getReplaceabilityClass(event.kind);

    if (klass === 'regular') {
      return { action: 'store', class: 'regular', key: null, event, incumbent: null };
    }
    if (klass === 'ephemeral') {
      return { action: 'ephemeral', class: 'ephemeral', key: null, event, incumbent: null };
    }

    const key = getStorageKey(event) as string;
    const incumbent = this.#byKey.get(key) ?? null;

    if (incumbent === null) {
      this.#byKey.set(key, event);
      return { action: 'store', class: klass, key, event, incumbent: null };
    }
    if (incumbent.id === event.id) {
      return { action: 'duplicate', class: klass, key, event, incumbent };
    }
    if (shouldReplace(incumbent, event)) {
      this.#byKey.set(key, event);
      return { action: 'replace', class: klass, key, event, incumbent };
    }
    return { action: 'reject_stale', class: klass, key, event, incumbent };
  }
}
