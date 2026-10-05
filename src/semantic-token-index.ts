/**
 * Versioned semantic token index.
 *
 * Each call to `update` re-analyzes the source, stores an immutable token
 * snapshot as a new version, and returns the minimal interval replacement
 * list (patches) that transforms the previous version into the new one.
 * Applying the returned patches to the previous version's tokens always
 * reproduces the new full token list exactly.
 */

import { ScalarText } from './scalar-text.ts';
import { analyzeSource, assertValidTokens } from './analyzer.ts';
import type { SemanticToken } from './analyzer.ts';
import { computePatches, applyPatches } from './diff.ts';
import type { TokenPatch } from './diff.ts';

export interface VersionDelta {
  /** Version the patches apply to (-1 denotes the empty initial state). */
  fromVersion: number;
  /** Version produced by applying the patches. */
  toVersion: number;
  /** Minimal interval replacement list, sorted by `start`. */
  patches: TokenPatch[];
}

export class SemanticTokenIndex {
  private readonly versions: SemanticToken[][] = [];
  /** edits[i] transforms version i-1 into version i (edits[0]: empty -> v0). */
  private readonly edits: TokenPatch[][] = [];

  constructor() {
    this.versions.push(Object.freeze([]) as SemanticToken[]);
  }

  /** Current version number; -1 before the first update. */
  get version(): number {
    return this.versions.length - 2;
  }

  /** Analyze `source` as a new version and return the delta from the previous one. */
  update(source: string | ScalarText): VersionDelta {
    const text = typeof source === 'string' ? ScalarText.from(source) : source;
    const tokens = analyzeSource(text);
    assertValidTokens(tokens);

    const previous = this.versions[this.versions.length - 1];
    const patches = computePatches(previous, tokens);

    this.versions.push(Object.freeze(tokens.map((t) => Object.freeze({ ...t }))) as SemanticToken[]);
    this.edits.push(patches);

    return { fromVersion: this.versions.length - 3, toVersion: this.versions.length - 2, patches };
  }

  /** Full token list of a version (defaults to the current one). */
  tokens(version?: number): readonly SemanticToken[] {
    const v = version ?? this.version;
    const index = v + 1;
    if (!Number.isInteger(v) || index < 0 || index >= this.versions.length) {
      throw new RangeError(`version ${v} out of range [-1, ${this.version}]`);
    }
    return this.versions[index];
  }

  /** Patches transforming `fromVersion` into `fromVersion + 1`. */
  delta(fromVersion: number): VersionDelta {
    const index = fromVersion + 1;
    if (!Number.isInteger(fromVersion) || index < 0 || index >= this.edits.length) {
      throw new RangeError(
        `no adjacent delta from version ${fromVersion}; valid range is [-1, ${this.version - 1}]`,
      );
    }
    return { fromVersion, toVersion: fromVersion + 1, patches: this.edits[index] };
  }

  /**
   * Apply a replacement list to a token array and validate the result
   * (sorted, non-overlapping). Exposed so consumers can maintain their own
   * token snapshot incrementally.
   */
  static applyPatches(
    base: readonly SemanticToken[],
    patches: readonly TokenPatch[],
  ): SemanticToken[] {
    const result = applyPatches(base, patches);
    assertValidTokens(result);
    return result;
  }
}
