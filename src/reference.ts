import { comparePrecedence, coversValidTime, isVisibleAt } from "./types.ts";
import type {
  Attributes,
  QueryResult,
  RecordInput,
  Revision,
} from "./types.ts";

/**
 * Brute-force reference implementation. Every query scans all revisions
 * one by one. Used as the ground truth to cross-check the indexed store.
 */
export class ReferenceStore {
  private readonly revisions: Revision[] = [];
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
    this.revisions.push(revision);
    return revision;
  }

  /** Revoke a revision; only knowledge versions >= the revoke version see it. */
  revoke(revisionId: number): Revision {
    const revision = this.revisions.find((r) => r.revisionId === revisionId);
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
    let winner: Revision | null = null;
    for (const revision of this.revisions) {
      if (revision.fileId !== fileId) continue;
      if (!coversValidTime(revision, validTime)) continue;
      if (!isVisibleAt(revision, knownAt)) continue;
      if (winner === null || comparePrecedence(revision, winner) > 0) {
        winner = revision;
      }
    }
    if (winner === null) return null;
    return {
      fileId,
      attributes: winner.attributes as Attributes,
      source: winner,
    };
  }
}
