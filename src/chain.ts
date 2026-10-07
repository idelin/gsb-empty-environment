import { mapOffsetThroughEdit, mapRangeThroughEdit, type RangeMapping } from "./mapping.ts";
import { edit as makeEdit, type BoundaryAffinity, type Range, type RangeAffinity, type TextEdit } from "./types.ts";

/**
 * Append-only log of edits, one edit per version step: applying `edits[i]`
 * transforms version `baseVersion + i` into `baseVersion + i + 1`.
 *
 * The chain is the source of truth for checking whether a completion
 * response computed against an old version is still applicable, and for
 * mapping its ranges forward to the current version.
 */
export class EditChain {
  private readonly edits: TextEdit[] = [];
  readonly baseVersion: number;

  constructor(baseVersion: number = 0) {
    this.baseVersion = baseVersion;
  }

  get currentVersion(): number {
    return this.baseVersion + this.edits.length;
  }

  /** Number of edits recorded so far. */
  get length(): number {
    return this.edits.length;
  }

  /** Append an edit (expressed against the current version) and return the new version. */
  record(edit: TextEdit): number {
    this.edits.push(edit);
    return this.currentVersion;
  }

  /** Whether a version can serve as a mapping source or target. */
  hasVersion(version: number): boolean {
    return Number.isInteger(version) && version >= this.baseVersion && version <= this.currentVersion;
  }

  /** The edit that transforms `version` into `version + 1`. */
  editAt(version: number): TextEdit {
    if (!this.hasVersion(version) || version === this.currentVersion) {
      throw new RangeError(`no edit recorded at version ${version}`);
    }
    return this.edits[version - this.baseVersion]!;
  }

  /**
   * Map a range from `fromVersion` to `toVersion` (defaults to current).
   * Returns { valid: false } if any edit in between invalidates the range.
   */
  mapRange(target: Range, fromVersion: number, affinity: RangeAffinity, toVersion?: number): RangeMapping {
    const to = toVersion ?? this.currentVersion;
    this.assertMappable(fromVersion);
    this.assertMappable(to);
    if (to < fromVersion) {
      throw new RangeError(`cannot map backwards: ${fromVersion} -> ${to}`);
    }
    let current: Range = target;
    for (let version = fromVersion; version < to; version += 1) {
      const result = mapRangeThroughEdit(current, this.editAt(version), affinity);
      if (!result.valid) return { valid: false };
      current = result.range;
    }
    return { valid: true, range: current };
  }

  /** Map a single offset from `fromVersion` to the current version. */
  mapOffset(offset: number, fromVersion: number, affinity: BoundaryAffinity, toVersion?: number): number {
    const to = toVersion ?? this.currentVersion;
    this.assertMappable(fromVersion);
    this.assertMappable(to);
    let current = offset;
    for (let version = fromVersion; version < to; version += 1) {
      current = mapOffsetThroughEdit(current, this.editAt(version), affinity);
    }
    return current;
  }

  private assertMappable(version: number): void {
    if (!this.hasVersion(version)) {
      throw new RangeError(
        `version ${version} is outside the tracked window [${this.baseVersion}, ${this.currentVersion}]`,
      );
    }
  }
}

/**
 * A text document whose every mutation is recorded in an EditChain,
 * giving each intermediate state a monotone version number.
 */
export class VersionedDocument {
  private text: string;
  readonly chain: EditChain;

  constructor(initialText = "") {
    this.text = initialText;
    this.chain = new EditChain(0);
  }

  get version(): number {
    return this.chain.currentVersion;
  }

  getText(): string {
    return this.text;
  }

  get length(): number {
    return this.text.length;
  }

  /** Apply one edit against the current version; returns the new version. */
  applyEdit(edit: TextEdit): number {
    if (edit.end > this.text.length) {
      throw new RangeError(`edit span [${edit.start}, ${edit.end}) exceeds document length ${this.text.length}`);
    }
    this.text = this.text.slice(0, edit.start) + edit.text + this.text.slice(edit.end);
    return this.chain.record(edit);
  }

  /** Convenience: insert `text` at `offset`. */
  insert(offset: number, text: string): number {
    return this.applyEdit(makeEdit(offset, offset, text));
  }

  /** Convenience: delete the span [start, end). */
  delete(start: number, end: number): number {
    return this.applyEdit(makeEdit(start, end, ""));
  }
}
