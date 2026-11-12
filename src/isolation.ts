import { InvalidSyncTokenError, SyncHistoryExpiredError } from './errors.ts';
import {
  isDirectMember,
  normalizeCollectionPath,
  normalizeHref,
} from './href.ts';
import type { NormalizedHref } from './href.ts';
import type {
  AddressbookCollection,
  AddressbookMember,
  AddressbookStore,
  SyncChange,
} from './store.ts';
import type { SyncTokenCodec } from './sync-token.ts';

export interface ReadAuthorization {
  canReadCollection(
    principalId: string,
    collection: AddressbookCollection,
  ): boolean;
  canReadMember(
    principalId: string,
    collection: AddressbookCollection,
    member: AddressbookMember,
  ): boolean;
}

export class DefaultReadAuthorization implements ReadAuthorization {
  canReadCollection(
    principalId: string,
    collection: AddressbookCollection,
  ): boolean {
    return (
      principalId === collection.ownerPrincipalId ||
      collection.readableBy.includes(principalId)
    );
  }

  canReadMember(
    principalId: string,
    collection: AddressbookCollection,
    member: AddressbookMember,
  ): boolean {
    if (!this.canReadCollection(principalId, collection)) {
      return false;
    }
    if (principalId === collection.ownerPrincipalId) {
      return true;
    }
    if (member.readableBy === undefined) {
      return true;
    }
    return member.readableBy.includes(principalId);
  }
}

export const defaultReadAuthorization = new DefaultReadAuthorization();

export interface MultigetRequest {
  readonly principalId: string;
  /** REPORT target path, with or without a trailing slash. */
  readonly collectionPath: string;
  readonly hrefs: readonly string[];
  /** Request URL authority, used to validate absolute DAV hrefs. */
  readonly requestAuthority?: string;
}

export interface MultigetFoundItem {
  readonly status: 200;
  readonly href: string;
  readonly member: AddressbookMember;
}

/**
 * Every successful normalization/membership/ACL failure uses this exact
 * shape. In particular, a cross-collection href, a deleted member and a
 * never-existing member are indistinguishable inside the 207 response.
 */
export interface MultigetNotFoundItem {
  readonly status: 404;
  readonly href: string;
}

export type MultigetItem = MultigetFoundItem | MultigetNotFoundItem;

export interface MultigetReport {
  readonly status: 207;
  readonly items: readonly MultigetItem[];
}

/** Top-level failure when the REPORT target itself is unavailable. */
export interface CollectionUnavailable {
  readonly status: 404;
  readonly error: 'collection-unavailable';
}

export interface SyncCollectionRequest {
  readonly principalId: string;
  readonly collectionPath: string;
  /**
   * Omit or send an empty token for the initial full sync. Non-empty tokens are
   * verified before any change log is read.
   */
  readonly syncToken?: string | null;
  readonly requestAuthority?: string;
}

export interface SyncFoundItem {
  readonly status: 200;
  readonly href: string;
  readonly member: AddressbookMember;
}

/**
 * A deletion is returned as 404 with the tombstone href, as required by
 * RFC 6578. It is reachable only through a token bound to this collection,
 * this principal and a revision at or before the deletion.
 */
export interface SyncDeletedItem {
  readonly status: 404;
  readonly href: string;
  readonly deleted: true;
  readonly revision: number;
}

export type SyncItem = SyncFoundItem | SyncDeletedItem;

export interface SyncCollectionReport {
  readonly status: 207;
  readonly syncToken: string;
  readonly changes: readonly SyncItem[];
}

export interface ValidSyncTokenFailure {
  readonly status: 403;
  readonly precondition: 'valid-sync-token';
  readonly error: 'valid-sync-token';
}

export const VALID_SYNC_TOKEN_FAILURE: ValidSyncTokenFailure = Object.freeze({
  status: 403,
  precondition: 'valid-sync-token',
  error: 'valid-sync-token',
});

export const VALID_SYNC_TOKEN_ERROR_XML =
  '<?xml version="1.0" encoding="utf-8"?>' +
  '<D:error xmlns:D="DAV:"><D:valid-sync-token/></D:error>';

export type MultigetResult = MultigetReport | CollectionUnavailable;
export type SyncCollectionResult =
  | SyncCollectionReport
  | ValidSyncTokenFailure
  | CollectionUnavailable;

export interface CardDavCollectionIsolationOptions {
  readonly store: AddressbookStore;
  readonly tokens: SyncTokenCodec;
  readonly authorization?: ReadAuthorization;
}

interface ResolvedCollection {
  readonly collection: AddressbookCollection;
  readonly path: NormalizedHref;
}

/**
 * Isolation boundary for `addressbook-multiget` and `sync-collection` REPORT
 * handlers. The handler supplies the authenticated principal and the request
 * collection from the request URL; this class supplies the per-href and
 * per-token checks that keep all returned data inside that collection.
 */
export class CardDavCollectionIsolation {
  readonly #store: AddressbookStore;
  readonly #tokens: SyncTokenCodec;
  readonly #authorization: ReadAuthorization;

  constructor(options: CardDavCollectionIsolationOptions) {
    this.#store = options.store;
    this.#tokens = options.tokens;
    this.#authorization = options.authorization ?? defaultReadAuthorization;
  }

  multiget(request: MultigetRequest): MultigetResult {
    const resolved = this.#resolveCollection(request);
    if (resolved === undefined) {
      return COLLECTION_UNAVAILABLE;
    }

    const items: MultigetItem[] = request.hrefs.map((href) => {
      const echo = safeEchoHref(href);

      try {
        const target = normalizeHref(href, {
          requestAuthority: request.requestAuthority,
        });

        if (!isDirectMember(resolved.path, target)) {
          return notFound(echo);
        }

        const member = this.#store.getMember(
          resolved.collection.id,
          target.path,
        );
        if (
          member === undefined ||
          member.collectionId !== resolved.collection.id ||
          member.href !== target.path
        ) {
          return notFound(echo);
        }

        if (
          !this.#authorization.canReadMember(
            request.principalId,
            resolved.collection,
            member,
          )
        ) {
          return notFound(echo);
        }

        return {
          status: 200,
          href: target.path,
          member,
        };
      } catch {
        return notFound(echo);
      }
    });

    return { status: 207, items };
  }

  syncCollection(request: SyncCollectionRequest): SyncCollectionResult {
    const resolved = this.#resolveCollection(request);
    if (resolved === undefined) {
      return COLLECTION_UNAVAILABLE;
    }

    const { collection } = resolved;
    const toRevision = collection.revision;

    const rawToken = request.syncToken;
    const tokenText =
      typeof rawToken === 'string' ? rawToken.trim() : '';
    const initial =
      rawToken === undefined ||
      rawToken === null ||
      tokenText.length === 0;

    if (!initial && typeof rawToken !== 'string') {
      return VALID_SYNC_TOKEN_FAILURE;
    }

    let changes: SyncItem[];

    if (initial) {
      changes = this.#readableMembers(
        collection,
        resolved.path,
        request.principalId,
        this.#store.listMembers(collection.id),
      );
    } else {
      let claims;
      try {
        claims = this.#tokens.verify(tokenText, {
          collectionId: collection.id,
          collectionPath: collection.path,
          principalId: request.principalId,
          minRevision: collection.earliestRevision,
          maxRevision: toRevision,
        });
      } catch (error) {
        if (error instanceof InvalidSyncTokenError) {
          return VALID_SYNC_TOKEN_FAILURE;
        }
        throw error;
      }

      let events: readonly SyncChange[];
      try {
        events = this.#store.changesSince(collection.id, claims.revision);
      } catch (error) {
        if (error instanceof SyncHistoryExpiredError) {
          return VALID_SYNC_TOKEN_FAILURE;
        }
        throw error;
      }

      changes = this.#changesToItems(
        collection,
        resolved.path,
        request.principalId,
        events,
        toRevision,
      );
    }

    const syncToken = this.#tokens.issue({
      collectionId: collection.id,
      collectionPath: collection.path,
      principalId: request.principalId,
      revision: toRevision,
    });

    return { status: 207, syncToken, changes };
  }

  #resolveCollection(
    request: Pick<MultigetRequest, 'principalId' | 'collectionPath' | 'requestAuthority'>,
  ): ResolvedCollection | undefined {
    let path: NormalizedHref;
    try {
      path = normalizeCollectionPath(request.collectionPath, {
        requestAuthority: request.requestAuthority,
      });
    } catch {
      return undefined;
    }

    const collection = this.#store.getCollectionByPath(path.path);
    if (collection === undefined) {
      return undefined;
    }

    let storedPath: NormalizedHref;
    try {
      storedPath = normalizeCollectionPath(collection.path);
    } catch {
      return undefined;
    }
    if (storedPath.path !== path.path) {
      return undefined;
    }

    const canonicalCollection: AddressbookCollection =
      collection.path === storedPath.path
        ? collection
        : { ...collection, path: storedPath.path };

    if (
      !this.#authorization.canReadCollection(
        request.principalId,
        canonicalCollection,
      )
    ) {
      return undefined;
    }

    return { collection: canonicalCollection, path: storedPath };
  }

  #readableMembers(
    collection: AddressbookCollection,
    collectionPath: NormalizedHref,
    principalId: string,
    members: readonly AddressbookMember[],
  ): SyncFoundItem[] {
    const items: SyncFoundItem[] = [];

    for (const member of members) {
      if (!this.#isInScopeMember(collection, collectionPath, member)) {
        continue;
      }
      if (!this.#authorization.canReadMember(principalId, collection, member)) {
        continue;
      }
      items.push({
        status: 200,
        href: member.href,
        member,
      });
    }

    return items;
  }

  #changesToItems(
    collection: AddressbookCollection,
    collectionPath: NormalizedHref,
    principalId: string,
    events: readonly SyncChange[],
    toRevision: number,
  ): SyncItem[] {
    const items: SyncItem[] = [];

    for (const event of events) {
      if (
        event.collectionId !== collection.id ||
        event.revision <= 0 ||
        event.revision > toRevision ||
        !this.#isInScopeHref(collectionPath, event.href)
      ) {
        continue;
      }

      if (event.kind === 'deleted') {
        if (this.#canReadDeletion(collection, principalId, event)) {
          items.push({
            status: 404,
            href: event.href,
            deleted: true,
            revision: event.revision,
          });
        }
        continue;
      }

      const member = this.#store.getMember(collection.id, event.href);
      if (
        member === undefined ||
        !this.#authorization.canReadMember(principalId, collection, member)
      ) {
        continue;
      }

      items.push({
        status: 200,
        href: member.href,
        member,
      });
    }

    return items;
  }

  #isInScopeMember(
    collection: AddressbookCollection,
    collectionPath: NormalizedHref,
    member: AddressbookMember,
  ): boolean {
    if (member.collectionId !== collection.id) {
      return false;
    }
    return this.#isInScopeHref(collectionPath, member.href);
  }

  #isInScopeHref(collectionPath: NormalizedHref, href: string): boolean {
    try {
      const target = normalizeHref(href);
      return target.path === href && isDirectMember(collectionPath, target);
    } catch {
      return false;
    }
  }

  #canReadDeletion(
    collection: AddressbookCollection,
    principalId: string,
    event: SyncChange,
  ): boolean {
    if (principalId === collection.ownerPrincipalId) {
      return true;
    }
    if (event.readableBy === undefined) {
      return true;
    }
    return event.readableBy.includes(principalId);
  }
}

export { CardDavCollectionIsolation as CollectionIsolation };

const COLLECTION_UNAVAILABLE: CollectionUnavailable = Object.freeze({
  status: 404,
  error: 'collection-unavailable',
});

function notFound(href: string): MultigetNotFoundItem {
  return { status: 404, href };
}

function safeEchoHref(href: unknown): string {
  if (typeof href !== 'string') {
    return '';
  }
  return href.trim().slice(0, 8192);
}
