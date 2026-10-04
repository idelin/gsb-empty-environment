import { SourceText } from './sourceText';
import { analyze, assertWellFormed, SemanticToken } from './analyzer';
import { diffTokens, TokenPatch } from './diff';

interface Version {
  source: SourceText;
  tokens: SemanticToken[];
}

/**
 * Versioned semantic token index. Each call to {@link update} snapshots a new
 * version and returns the minimal interval replacement list that transforms
 * the previous version's tokens into the new ones, so remote caches can be
 * updated incrementally instead of re-pulling the full token list.
 */
export class SemanticTokenIndex {
  private readonly versions: Version[] = [];

  constructor(initialSource: string) {
    this.update(initialSource);
  }

  /** Current version number (0-based; increments on every update). */
  get version(): number {
    return this.versions.length - 1;
  }

  getSource(version = this.version): SourceText {
    return this.versions[version].source;
  }

  /** Full semantic token list for a version (UTF-16 offsets, non-overlapping). */
  getTokens(version = this.version): SemanticToken[] {
    return this.versions[version].tokens;
  }

  /**
   * Snapshot a new source version. Returns the minimal replacement list
   * between the previous version and the new one.
   */
  update(source: string): TokenPatch[] {
    const text = new SourceText(source);
    const tokens = analyze(text);
    assertWellFormed(tokens);
    const previous = this.versions.length > 0 ? this.versions[this.versions.length - 1].tokens : [];
    this.versions.push({ source: text, tokens });
    return diffTokens(previous, tokens);
  }

  /**
   * Minimal replacement list between two recorded versions (typically
   * adjacent ones).
   */
  diff(from: number, to: number): TokenPatch[] {
    if (from < 0 || to < 0 || from >= this.versions.length || to >= this.versions.length) {
      throw new RangeError(`version out of range [0, ${this.version}]`);
    }
    return diffTokens(this.versions[from].tokens, this.versions[to].tokens);
  }
}
