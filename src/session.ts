/**
 * A linear-version session: the authoritative document plus the history of
 * transformed operations. Incoming operations carry the version they were
 * computed against (`baseVersion`); older baselines are transformed forward
 * through the history, invalid baselines are rejected.
 */

import { applyOps, transformList } from './ot.ts';
import type { SiteId, TextOp, VersionedOp } from './ot.ts';

interface HistoryEntry {
  ops: TextOp[];
  siteId: SiteId;
}

export class Session {
  #doc: string;
  /** history[v] is the (already transformed) op list applied at version v. */
  #history: HistoryEntry[] = [];

  constructor(initialDoc = '') {
    this.#doc = initialDoc;
  }

  get doc(): string {
    return this.#doc;
  }

  get version(): number {
    return this.#history.length;
  }

  /** Validates the baseline of an incoming op. Throws on invalid baselines. */
  checkBaseline(baseVersion: number): void {
    if (!Number.isInteger(baseVersion)) {
      throw new RangeError(`baseVersion ${baseVersion} is not an integer`);
    }
    if (baseVersion < 0) {
      throw new RangeError(`baseVersion ${baseVersion} is negative`);
    }
    if (baseVersion > this.version) {
      throw new RangeError(
        `baseVersion ${baseVersion} is ahead of current version ${this.version}`,
      );
    }
  }

  /**
   * Receives an op produced against `baseVersion`, transforms it forward over
   * everything that happened since, applies it, and records it. Returns the
   * transformed op list (what the other sites should apply).
   */
  receive(incoming: VersionedOp): TextOp[] {
    this.checkBaseline(incoming.baseVersion);
    let ops: TextOp[] = [incoming.op];
    for (let v = incoming.baseVersion; v < this.version; v++) {
      const entry = this.#history[v];
      for (const other of entry.ops) {
        ops = transformList(ops, incoming.siteId, other, entry.siteId);
      }
    }
    this.#doc = applyOps(this.#doc, ops);
    this.#history.push({ ops, siteId: incoming.siteId });
    return ops;
  }
}
