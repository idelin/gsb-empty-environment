import { comparePrecedence, isVisibleAt } from "./types.ts";
import type { QueryResult, RecordInput, Revision } from "./types.ts";

/**
 * Per-file updatable index.
 *
 * Revisions are kept in an array sorted by (validFrom, validTo, revisionId).
 * Inserts and revocations are O(n) pointer updates / splice; queries binary
 * search the largest validFrom <= validTime and scan that prefix backwards,
 * filtering on interval end and knowledge-version visibility.
 */
class FileIndex {
  /** Sorted by (validFrom, validTo, revisionId). */
  private readonly sorted: Revision[] = [];
  private readonly byId = new Map<number, Revision>();

  private static order(a: Revision, b: Revision): number {
    if (a.validFrom !== b.validFrom) return a.validFrom - b.validFrom;
    if (a.validTo !== b.validTo) return a.validTo - b.validTo;
    return a.revisionId - b.revisionId;
  }

  insert(revision: Revision): void {
    let lo = 0;
    let hi = this.sorted.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      const pivot = this.sorted[mid];
      if (pivot !== undefined && FileIndex.order(pivot, revision) < 0) {
        lo = mid + 1;
      } else {
        hi = mid;
      }
    }
    this.sorted.splice(lo, 0, revision);
    this.byId.set(revision.revisionId, revision);
  }

  get(revisionId: number): Revision | undefined {
    return this.byId.get(revisionId);
  }

  /**
   * Find the best revision covering `validTime` and visible at `knownAt`.
   * Only revisions with validFrom <= validTime can match, so the search is
   * confined to the sorted prefix found by binary search.
   */
  query(validTime: number, knownAt: number): Revision | null {
    // Rightmost index with validFrom <= validTime.
    let lo = 0;
    let hi = this.sorted.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      const pivot = this.sorted[mid];
      if (pivot !== undefined && pivot.validFrom <= validTime) {
        lo = mid + 1;
      } else {
        hi = mid;
      }
    }
    let winner: Revision | null = null;
    for (let i = lo - 1; i >= 0; i--) {
      const revision = this.sorted[i];
      if (revision === undefined) continue;
      if (revision.validTo <= validTime) continue; // half-open: end excluded
      if (!isVisibleAt(revision, knownAt)) continue;
      if (winner === null || comparePrecedence(revision, winner) > 0) {
        winner = revision;
      }
    }
    return winner;
  }
}

/**
 * Indexed bitemporal file history store.
 *
 * - record(): appends a revision at a new knowledge version. Late backfills
 *   (valid interval in the past, recorded now) are ordinary inserts.
 * - revoke(): stamps the revision with the current+1 knowledge version;
 *   queries at earlier knowledge versions still see it.
 * - query(): resolves conflicts by highest knowledge version, then highest
 *   revisionId, and returns the winning attributes plus the source revision.
 */
export class BitemporalFileStore {
  private readonly files = new Map<string, FileIndex>();
  private readonly byId = new Map<number, Revision>();
  private knowledgeVersion = 0;
  private nextRevisionId = 1;

  get currentVersion(): number {
    return this.knowledgeVersion;
  }

  record(input: RecordInput): Revision {
    if (!(input.validFrom < input.validTo)) {
      throw new RangeError(
        `invalid interval [${input.validFrom}, ${input.validTo})`,
      );
    }
    const revision: Revision = {
      revisionId: this.nextRevisionId++,
      fileId: input.fileId,
      attributes: { ...input.attributes },
      validFrom: input.validFrom,
      validTo: input.validTo,
      recordedAt: ++this.knowledgeVersion,
      revokedAt: null,
    };
    let index = this.files.get(input.fileId);
    if (index === undefined) {
      index = new FileIndex();
      this.files.set(input.fileId, index);
    }
    index.insert(revision);
    this.byId.set(revision.revisionId, revision);
    return revision;
  }

  /**
   * Revoke a revision. The revocation takes effect at a new knowledge
   * version; queries pinned to earlier knowledge versions are unaffected.
   */
  revoke(revisionId: number): Revision {
    const revision = this.byId.get(revisionId);
    if (revision === undefined) {
      throw new Error(`unknown revisionId ${revisionId}`);
    }
    if (revision.revokedAt !== null) {
      throw new Error(`revision ${revisionId} already revoked`);
    }
    revision.revokedAt = ++this.knowledgeVersion;
    return revision;
  }

  query(fileId: string, validTime: number, knownAt: number): QueryResult | null {
    const index = this.files.get(fileId);
    if (index === undefined) return null;
    const winner = index.query(validTime, knownAt);
    if (winner === null) return null;
    return {
      fileId,
      attributes: winner.attributes,
      source: winner,
    };
  }
}
