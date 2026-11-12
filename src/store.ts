import { SyncHistoryExpiredError } from './errors.ts';
import { normalizeCollectionPath, normalizeHref, isDirectMember } from './href.ts';

export interface AddressbookCollection {
  readonly id: string;
  /** Canonical absolute collection path, without a trailing slash. */
  readonly path: string;
  readonly ownerPrincipalId: string;
  readonly readableBy: readonly string[];
  readonly revision: number;
  readonly earliestRevision: number;
}

export interface AddressbookMember {
  readonly collectionId: string;
  /** Canonical absolute member href produced by `normalizeHref`. */
  readonly href: string;
  readonly etag: string;
  readonly contentType: string;
  readonly data: string | Uint8Array;
  readonly revision: number;
  /** Optional per-member ACL. Omit to inherit collection read access. */
  readonly readableBy?: readonly string[];
}

export type SyncChangeKind = 'created' | 'updated' | 'deleted';

export interface SyncChange {
  readonly collectionId: string;
  /** Canonical absolute href, including the tombstone href for deletions. */
  readonly href: string;
  readonly revision: number;
  readonly kind: SyncChangeKind;
  /** Former ACL, retained for deletions so a shared collection can filter. */
  readonly readableBy?: readonly string[];
}

/**
 * Storage boundary used by the isolation package. A production implementation
 * should return a consistent collection snapshot (revision, member list and
 * change log) from one database transaction.
 */
export interface AddressbookStore {
  getCollectionByPath(path: string): AddressbookCollection | undefined;
  getCollectionById(id: string): AddressbookCollection | undefined;
  getMember(collectionId: string, canonicalHref: string): AddressbookMember | undefined;
  listMembers(collectionId: string): readonly AddressbookMember[];
  /** Changes strictly after `revision`, aggregated to the final state per href. */
  changesSince(collectionId: string, revision: number): readonly SyncChange[];
}

export interface AddCollectionInput {
  readonly id: string;
  readonly path: string;
  readonly ownerPrincipalId: string;
  readonly readableBy?: readonly string[];
  readonly revision?: number;
  readonly earliestRevision?: number;
}

export interface PutMemberInput {
  readonly href: string;
  readonly etag?: string;
  readonly contentType?: string;
  readonly data?: string | Uint8Array;
  readonly readableBy?: readonly string[];
}

interface CollectionState {
  collection: AddressbookCollection;
  members: Map<string, AddressbookMember>;
  events: SyncChange[];
  tombstones: Map<string, SyncChange>;
}

/**
 * Small deterministic store for tests, examples and local development. Every
 * put/delete advances the collection revision by one and appends a change
 * event, so token swap and deleted-member tests exercise the same paths as a
 * database-backed store.
 */
export class InMemoryAddressbookStore implements AddressbookStore {
  readonly #byPath = new Map<string, CollectionState>();
  readonly #byId = new Map<string, CollectionState>();

  addCollection(input: AddCollectionInput): AddressbookCollection {
    if (typeof input.id !== 'string' || input.id.length === 0) {
      throw new TypeError('collection id must not be empty');
    }
    if (typeof input.ownerPrincipalId !== 'string' || input.ownerPrincipalId.length === 0) {
      throw new TypeError('ownerPrincipalId must not be empty');
    }

    const normalizedPath = normalizeCollectionPath(input.path).path;
    if (this.#byPath.has(normalizedPath)) {
      throw new Error(`collection path already exists: ${normalizedPath}`);
    }
    if (this.#byId.has(input.id)) {
      throw new Error(`collection id already exists: ${input.id}`);
    }

    const revision = input.revision ?? 0;
    const earliestRevision = input.earliestRevision ?? 0;
    if (!Number.isSafeInteger(revision) || revision < 0) {
      throw new TypeError('collection revision must be a non-negative safe integer');
    }
    if (
      !Number.isSafeInteger(earliestRevision) ||
      earliestRevision < 0 ||
      earliestRevision > revision
    ) {
      throw new TypeError('earliestRevision must be between 0 and revision');
    }

    const collection: AddressbookCollection = {
      id: input.id,
      path: normalizedPath,
      ownerPrincipalId: input.ownerPrincipalId,
      readableBy: input.readableBy ? [...input.readableBy] : [input.ownerPrincipalId],
      revision,
      earliestRevision,
    };

    const state: CollectionState = {
      collection,
      members: new Map(),
      events: [],
      tombstones: new Map(),
    };

    this.#byPath.set(normalizedPath, state);
    this.#byId.set(input.id, state);
    return copyCollection(collection);
  }

  putMember(collectionId: string, input: PutMemberInput): AddressbookMember {
    const state = this.#requireState(collectionId);
    const collection = normalizeCollectionPath(state.collection.path);
    const target = normalizeHref(input.href);

    if (!isDirectMember(collection, target)) {
      throw new Error('member href must be a direct child of the collection');
    }

    const revision = state.collection.revision + 1;
    const previous = state.members.get(target.path);
    const member: AddressbookMember = {
      collectionId,
      href: target.path,
      etag: input.etag ?? `"rev-${revision}"`,
      contentType: input.contentType ?? 'text/vcard; charset=utf-8',
      data: copyData(input.data ?? ''),
      revision,
      readableBy: input.readableBy ? [...input.readableBy] : previous?.readableBy,
    };

    state.members.set(member.href, member);
    state.tombstones.delete(member.href);
    state.events.push({
      collectionId,
      href: member.href,
      revision,
      kind: previous === undefined ? 'created' : 'updated',
      readableBy: member.readableBy,
    });
    this.#setRevision(state, revision);

    return copyMember(member);
  }

  deleteMember(collectionId: string, href: string): boolean {
    const state = this.#requireState(collectionId);
    const collection = normalizeCollectionPath(state.collection.path);
    const target = normalizeHref(href);

    if (!isDirectMember(collection, target)) {
      throw new Error('member href must be a direct child of the collection');
    }

    const member = state.members.get(target.path);
    if (member === undefined) {
      return false;
    }

    const revision = state.collection.revision + 1;
    state.members.delete(member.href);

    const tombstone: SyncChange = {
      collectionId,
      href: member.href,
      revision,
      kind: 'deleted',
      readableBy: member.readableBy,
    };
    state.tombstones.set(member.href, tombstone);
    state.events.push(tombstone);
    this.#setRevision(state, revision);
    return true;
  }

  pruneHistory(collectionId: string, beforeRevision: number): void {
    const state = this.#requireState(collectionId);
    if (
      !Number.isSafeInteger(beforeRevision) ||
      beforeRevision < state.collection.earliestRevision ||
      beforeRevision > state.collection.revision
    ) {
      throw new TypeError('beforeRevision must be within the collection revision range');
    }

    state.events = state.events.filter((event) => event.revision > beforeRevision);
    state.collection = {
      ...state.collection,
      earliestRevision: beforeRevision,
    };
  }

  getCollectionByPath(path: string): AddressbookCollection | undefined {
    let canonicalPath: string;
    try {
      canonicalPath = normalizeCollectionPath(path).path;
    } catch {
      return undefined;
    }

    const state = this.#byPath.get(canonicalPath);
    return state === undefined ? undefined : copyCollection(state.collection);
  }

  getCollectionById(id: string): AddressbookCollection | undefined {
    const state = this.#byId.get(id);
    return state === undefined ? undefined : copyCollection(state.collection);
  }

  getMember(collectionId: string, canonicalHref: string): AddressbookMember | undefined {
    const state = this.#byId.get(collectionId);
    if (state === undefined) {
      return undefined;
    }

    let canonicalPath: string;
    try {
      canonicalPath = normalizeHref(canonicalHref).path;
    } catch {
      return undefined;
    }

    const member = state.members.get(canonicalPath);
    return member === undefined ? undefined : copyMember(member);
  }

  listMembers(collectionId: string): readonly AddressbookMember[] {
    const state = this.#requireState(collectionId);
    return [...state.members.values()]
      .sort((left, right) =>
        left.href < right.href ? -1 : left.href > right.href ? 1 : 0,
      )
      .map(copyMember);
  }

  changesSince(collectionId: string, revision: number): readonly SyncChange[] {
    const state = this.#requireState(collectionId);
    if (!Number.isSafeInteger(revision) || revision < 0) {
      throw new TypeError('revision must be a non-negative safe integer');
    }
    if (revision > state.collection.revision) {
      throw new TypeError('revision is newer than the collection');
    }
    if (revision < state.collection.earliestRevision) {
      throw new SyncHistoryExpiredError();
    }

    const latest = new Map<string, SyncChange>();
    for (const event of state.events) {
      if (event.revision > revision) {
        latest.set(event.href, event);
      }
    }

    return [...latest.values()]
      .sort((left, right) => left.revision - right.revision)
      .map(copyChange);
  }

  #requireState(collectionId: string): CollectionState {
    const state = this.#byId.get(collectionId);
    if (state === undefined) {
      throw new Error(`unknown collection: ${collectionId}`);
    }
    return state;
  }

  #setRevision(state: CollectionState, revision: number): void {
    state.collection = {
      ...state.collection,
      revision,
    };
  }
}

function copyCollection(collection: AddressbookCollection): AddressbookCollection {
  return {
    ...collection,
    readableBy: [...collection.readableBy],
  };
}

function copyMember(member: AddressbookMember): AddressbookMember {
  return {
    ...member,
    data: copyData(member.data),
    readableBy: member.readableBy === undefined ? undefined : [...member.readableBy],
  };
}

function copyChange(change: SyncChange): SyncChange {
  return {
    ...change,
    readableBy: change.readableBy === undefined ? undefined : [...change.readableBy],
  };
}

function copyData(data: string | Uint8Array): string | Uint8Array {
  return typeof data === 'string' ? data : new Uint8Array(data);
}
