/**
 * IME-aware edit-control state machine.
 *
 * The controller is DOM-free: it consumes synthetic `composition`, `input`
 * and `selection` events (mirroring the DOM event vocabulary) and maintains
 * text, selection, undo/redo stacks and validation state.
 *
 * Invariants:
 *  - While an IME composition is active, text is NEVER truncated and
 *    validation is NEVER committed; the composition may temporarily exceed
 *    `maxLength`.
 *  - The length cap is applied uniformly once, at `compositionend`, and is
 *    counted in Unicode code points.
 *  - A whole composition session produces exactly ONE undo entry, capturing
 *    the pre-composition snapshot, so `undo()` restores the state from
 *    before the composition started.
 *  - A duplicate trailing `input` event after `compositionend` (Chrome
 *    fires `insertFromComposition` after `compositionend`) is swallowed so
 *    the committed text is never inserted twice.
 */

import { codePointLength, codePointSlice, spliceText } from './unicode.ts';

export type InputType =
  | 'insertText'
  | 'insertCompositionText'
  | 'insertFromComposition'
  | 'deleteContentBackward'
  | 'deleteContentForward';

export type EditorEvent =
  | { type: 'compositionstart'; data?: string }
  | { type: 'compositionupdate'; data: string }
  | { type: 'compositionend'; data: string }
  | { type: 'compositioncancel' }
  | { type: 'input'; inputType: InputType; data: string | null; isComposing: boolean }
  | { type: 'selectionchange'; anchor: number; focus: number };

export interface Snapshot {
  text: string;
  anchor: number;
  focus: number;
}

export interface EditorOptions {
  /** Maximum length in Unicode code points. Omit for no cap. */
  maxLength?: number;
  /** Returns an error message, or null when the text is valid. */
  validate?: (text: string) => string | null;
}

export interface EditorState {
  text: string;
  anchor: number;
  focus: number;
  composing: boolean;
  validationError: string | null;
  undoDepth: number;
  redoDepth: number;
}

interface CompositionCommit {
  /** Committed composition text as reported by `compositionend`. */
  data: string;
  /** Collapsed caret position right after the commit. */
  caret: number;
}

export class EditorController {
  private text = '';
  private anchor = 0;
  private focus = 0;

  private composing = false;
  private compStart = 0;
  private compEnd = 0;
  private preComposition: Snapshot | null = null;

  private lastCommit: CompositionCommit | null = null;

  private undoStack: Snapshot[] = [];
  private redoStack: Snapshot[] = [];
  private validationError: string | null = null;

  private readonly maxLength: number;
  private readonly validate?: (text: string) => string | null;

  constructor(options: EditorOptions = {}) {
    this.maxLength = options.maxLength ?? Number.POSITIVE_INFINITY;
    this.validate = options.validate;
  }

  getState(): EditorState {
    return {
      text: this.text,
      anchor: this.anchor,
      focus: this.focus,
      composing: this.composing,
      validationError: this.validationError,
      undoDepth: this.undoStack.length,
      redoDepth: this.redoStack.length,
    };
  }

  dispatch(event: EditorEvent): void {
    switch (event.type) {
      case 'compositionstart':
        this.startComposition();
        break;
      case 'compositionupdate':
        if (this.composing) this.updateComposition(event.data);
        break;
      case 'compositionend':
        if (this.composing) this.endComposition(event.data);
        break;
      case 'compositioncancel':
        if (this.composing) this.cancelComposition();
        break;
      case 'input':
        this.handleInput(event);
        break;
      case 'selectionchange':
        this.setSelection(event.anchor, event.focus);
        break;
    }
  }

  undo(): void {
    const previous = this.undoStack.pop();
    if (!previous) return;
    this.redoStack.push(this.snapshot());
    this.restore(previous);
    this.commitValidation();
  }

  redo(): void {
    const next = this.redoStack.pop();
    if (!next) return;
    this.undoStack.push(this.snapshot());
    this.restore(next);
    this.commitValidation();
  }

  // --- composition session -------------------------------------------------

  private startComposition(): void {
    this.lastCommit = null;
    this.preComposition = this.snapshot();
    // The composition replaces the current selection.
    const [start, end] = this.selectionRange();
    if (end > start) {
      this.text = spliceText(this.text, start, end - start, '');
    }
    this.compStart = start;
    this.compEnd = start;
    this.anchor = start;
    this.focus = start;
    this.composing = true;
  }

  private updateComposition(data: string): void {
    // No truncation, no validation, no undo entries while composing.
    this.text = spliceText(this.text, this.compStart, this.compEnd - this.compStart, data);
    this.compEnd = this.compStart + codePointLength(data);
    this.anchor = this.compEnd;
    this.focus = this.compEnd;
  }

  private endComposition(data: string): void {
    if (data === '') {
      // Empty commit (e.g. Esc in several browsers) cancels the composition.
      this.cancelComposition();
      return;
    }
    this.updateComposition(data);
    this.composing = false;

    // Uniform length cap, applied once at composition end. Only the freshly
    // committed composition text is truncated; pre-existing text is kept.
    const insertionLength = this.compEnd - this.compStart;
    const othersLength = codePointLength(this.text) - insertionLength;
    const allowed = Math.max(0, this.maxLength - othersLength);
    if (insertionLength > allowed) {
      const kept = codePointSlice(data, 0, allowed);
      this.text = spliceText(this.text, this.compStart, insertionLength, kept);
      this.compEnd = this.compStart + allowed;
      this.anchor = this.compEnd;
      this.focus = this.compEnd;
    }

    if (this.preComposition) {
      this.pushUndo(this.preComposition);
      this.preComposition = null;
    }
    this.lastCommit = { data, caret: this.focus };
    this.commitValidation();
  }

  private cancelComposition(): void {
    // Revert to the pre-composition snapshot; no undo entry, no validation.
    if (this.preComposition) {
      this.restore(this.preComposition);
      this.preComposition = null;
    }
    this.composing = false;
    this.lastCommit = null;
  }

  // --- input ----------------------------------------------------------------

  private handleInput(event: {
    inputType: InputType;
    data: string | null;
    isComposing: boolean;
  }): void {
    if (this.composing || event.isComposing || event.inputType === 'insertCompositionText') {
      if (!this.composing) this.startComposition();
      if (event.inputType === 'deleteContentBackward' || event.inputType === 'deleteContentForward') {
        this.deleteInsideComposition(event.inputType);
      } else {
        this.updateComposition(event.data ?? '');
      }
      return;
    }

    if (this.isDuplicateTrailingInput(event)) {
      this.lastCommit = null;
      return;
    }
    this.lastCommit = null;

    switch (event.inputType) {
      case 'insertText':
      case 'insertFromComposition':
        this.insertNormal(event.data ?? '');
        break;
      case 'deleteContentBackward':
        this.deleteNormal('backward');
        break;
      case 'deleteContentForward':
        this.deleteNormal('forward');
        break;
    }
  }

  /**
   * Chrome dispatches `compositionend` BEFORE the final `input`
   * (`insertFromComposition`, same data). That trailing event replays the
   * text we already committed, so it must be swallowed exactly once.
   */
  private isDuplicateTrailingInput(event: {
    inputType: InputType;
    data: string | null;
  }): boolean {
    return (
      this.lastCommit !== null &&
      event.inputType === 'insertFromComposition' &&
      event.data !== null &&
      event.data === this.lastCommit.data &&
      this.anchor === this.focus &&
      this.focus === this.lastCommit.caret
    );
  }

  private insertNormal(data: string): void {
    const [start, end] = this.selectionRange();
    const room = this.maxLength - (codePointLength(this.text) - (end - start));
    const insert = codePointSlice(data, 0, Math.max(0, room));
    if (end === start && insert === '') return;
    this.pushUndo(this.snapshot());
    this.text = spliceText(this.text, start, end - start, insert);
    this.setSelection(start + codePointLength(insert), start + codePointLength(insert));
    this.commitValidation();
  }

  private deleteNormal(direction: 'backward' | 'forward'): void {
    const [start, end] = this.selectionRange();
    let deleteStart = start;
    let deleteEnd = end;
    if (start === end) {
      if (direction === 'backward') {
        if (start === 0) return;
        deleteStart = start - 1;
      } else {
        if (start === codePointLength(this.text)) return;
        deleteEnd = start + 1;
      }
    }
    this.pushUndo(this.snapshot());
    this.text = spliceText(this.text, deleteStart, deleteEnd - deleteStart, '');
    this.setSelection(deleteStart, deleteStart);
    this.commitValidation();
  }

  private deleteInsideComposition(inputType: InputType): void {
    if (inputType === 'deleteContentBackward' && this.compEnd > this.compStart) {
      this.compEnd -= 1;
      this.text = spliceText(this.text, this.compEnd, 1, '');
      this.anchor = this.compEnd;
      this.focus = this.compEnd;
    }
  }

  // --- selection / history / validation ------------------------------------

  private selectionRange(): [number, number] {
    return this.anchor <= this.focus
      ? [this.anchor, this.focus]
      : [this.focus, this.anchor];
  }

  private setSelection(anchor: number, focus: number): void {
    const length = codePointLength(this.text);
    const clamp = (offset: number) => Math.max(0, Math.min(offset, length));
    this.anchor = clamp(anchor);
    this.focus = clamp(focus);
  }

  private snapshot(): Snapshot {
    return { text: this.text, anchor: this.anchor, focus: this.focus };
  }

  private restore(snapshot: Snapshot): void {
    this.text = snapshot.text;
    this.anchor = snapshot.anchor;
    this.focus = snapshot.focus;
  }

  private pushUndo(snapshot: Snapshot): void {
    this.undoStack.push(snapshot);
    this.redoStack = [];
  }

  private commitValidation(): void {
    this.validationError = this.validate ? this.validate(this.text) : null;
  }
}
