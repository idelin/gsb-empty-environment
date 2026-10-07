import { applyEdits, invertEdits } from './replace';
import { buildPositionMap } from './positionMap';
import { InverseEdit, PositionMap, TextEdit } from './types';

export interface AppliedChange {
  edits: TextEdit[];
  inverse: InverseEdit[];
  map: PositionMap;
  fromVersion: number;
  toVersion: number;
}

/**
 * A text document with a monotonically increasing version. Every mutation
 * goes through `applyEdits`, which bumps the version by exactly one, so a
 * version number uniquely identifies a frozen snapshot of the text.
 */
export class TextDocument {
  private _text: string;
  private _version: number;

  constructor(readonly uri: string, initialText: string, initialVersion = 0) {
    this._text = initialText;
    this._version = initialVersion;
  }

  get text(): string {
    return this._text;
  }

  get version(): number {
    return this._version;
  }

  /**
   * Apply non-overlapping edits simultaneously against the current frozen
   * text. Returns the forward edits, their inverse, and the offset mapping.
   */
  applyEdits(edits: TextEdit[]): AppliedChange {
    const oldText = this._text;
    const map = buildPositionMap(oldText.length, edits);
    const inverse = invertEdits(oldText, edits, map);
    this._text = applyEdits(oldText, edits);
    this._version += 1;
    return {
      edits: [...edits].sort((a, b) => a.start - b.start || a.end - b.end),
      inverse,
      map,
      fromVersion: this._version - 1,
      toVersion: this._version,
    };
  }

  /** Restore a previous snapshot. Only used by transaction undo. */
  restore(text: string, version: number): void {
    this._text = text;
    this._version = version;
  }
}
