/**
 * A replica (site) holding a document, its version counter, and the history
 * needed to transform incoming ops that were created against older versions.
 */
import {
  applyOp,
  InvalidBaseVersionError,
  transformAgainstAll,
  validateOp,
  type Op,
  type VersionedOp,
} from './ot.ts';

interface HistoryEntry {
  siteId: number;
  ops: readonly Op[];
}

export class Replica {
  readonly siteId: number;
  #text: string;
  #history: HistoryEntry[] = [];

  constructor(siteId: number, initialText = '') {
    this.siteId = siteId;
    this.#text = initialText;
  }

  get text(): string {
    return this.#text;
  }

  /** Current version: number of ops (local or remote) applied so far. */
  get version(): number {
    return this.#history.length;
  }

  /**
   * Apply a locally-created op. Returns the message to broadcast to peers;
   * it is stamped with the version the op was created against.
   */
  local(op: Op): VersionedOp {
    validateOp(this.#text, op);
    const message: VersionedOp = {
      baseVersion: this.version,
      siteId: this.siteId,
      op,
    };
    this.#text = applyOp(this.#text, op);
    this.#history.push({ siteId: this.siteId, ops: [op] });
    return message;
  }

  /**
   * Receive an op created on another site against `baseVersion`. The op is
   * transformed over everything applied here since that version, then
   * applied. Throws InvalidBaseVersionError for unusable base versions.
   */
  receive(message: VersionedOp): void {
    const { baseVersion } = message;
    if (
      !Number.isInteger(baseVersion) ||
      baseVersion < 0 ||
      baseVersion > this.version
    ) {
      throw new InvalidBaseVersionError(baseVersion, this.version);
    }
    const transformed = transformAgainstAll(
      message.op,
      this.#history.slice(baseVersion),
      message.siteId,
    );
    // Validate and apply step by step: positions of later ops (e.g. the
    // second half of a split delete) refer to intermediate document states.
    let text = this.#text;
    for (const op of transformed) {
      validateOp(text, op);
      text = applyOp(text, op);
    }
    this.#text = text;
    this.#history.push({ siteId: message.siteId, ops: transformed });
  }
}
