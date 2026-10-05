/**
 * Shared types for the bitemporal file history store.
 *
 * Two time axes:
 *  - valid time (业务生效区间): half-open interval [validFrom, validTo)
 *  - knowledge version (录入版本/知悉版本): monotonically increasing integer
 *    assigned by the store to every commit (record or revoke).
 */

export type Attributes = Record<string, unknown>;

/** A single recorded change of a file's attributes. */
export interface Revision {
  /** Unique, strictly increasing tiebreaker id assigned at commit time. */
  readonly revisionId: number;
  readonly fileId: string;
  readonly attributes: Attributes;
  /** Inclusive start of the business-effective interval. */
  readonly validFrom: number;
  /** Exclusive end of the business-effective interval. */
  readonly validTo: number;
  /** Knowledge version at which this revision was recorded. */
  readonly recordedAt: number;
  /**
   * Knowledge version at which this revision was revoked, or null while
   * active. A revocation is only visible to queries whose knowledge
   * version is >= revokedAt; earlier knowledge versions are unaffected.
   */
  revokedAt: number | null;
}

/** Result of a two-time query: the winning attributes plus provenance. */
export interface QueryResult {
  readonly fileId: string;
  readonly attributes: Attributes;
  /** The exact revision the answer was sourced from. */
  readonly source: Revision;
}

/** Input accepted when recording a new revision. */
export interface RecordInput {
  readonly fileId: string;
  readonly attributes: Attributes;
  readonly validFrom: number;
  readonly validTo: number;
}

export function isVisibleAt(revision: Revision, knownAt: number): boolean {
  if (revision.recordedAt > knownAt) return false;
  if (revision.revokedAt !== null && revision.revokedAt <= knownAt) return false;
  return true;
}

export function coversValidTime(revision: Revision, validTime: number): boolean {
  return revision.validFrom <= validTime && validTime < revision.validTo;
}

/**
 * Conflict ordering: higher knowledge version wins; ties are broken by
 * higher revisionId. Returns negative if `a` loses to `b`.
 */
export function comparePrecedence(a: Revision, b: Revision): number {
  if (a.recordedAt !== b.recordedAt) return a.recordedAt - b.recordedAt;
  return a.revisionId - b.revisionId;
}
