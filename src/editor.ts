/**
 * Composition (IME) editing state machine.
 *
 * Two lanes of state are maintained:
 *  - FORMAL: the committed document plus its undo history. Composition
 *    intermediate states NEVER enter the formal document or its history.
 *  - PROJECTION: a temporary view = formal document with the composition
 *    range replaced by the in-progress composition text.
 *
 * Public rules (deterministic):
 *  R1. Offsets are UTF-16 code unit indices; an offset that would split a
 *      surrogate pair is snapped down to the pair start (see utf16.ts).
 *  R2. compositionStart returns a generation id. update/commit/cancel carry
 *      a generation id; a stale id is a no-op returning false, so old
 *      callbacks can never pollute a newer composition.
 *  R3. Updates only touch the projection. Commit applies the final text to
 *      the formal document as exactly ONE undo unit. A repeated commit or a
 *      late update for a finished generation is a no-op (no double insert).
 *  R4. Cancel restores the pre-composition formal text (untouched, since
 *      updates never touched it) and the pre-composition selection. It never
 *      undoes remote/local edits that arrived during the composition.
 *  R5. External edit (remote/local/undo/redo) vs. active composition range
 *      [start, end) vs. edit range [s, e):
 *        - overlap  (s < end && start < e)  => CONFLICT: the composition is
 *          cancelled (external edit wins) and the caret moves to the end of
 *          the applied external edit.
 *        - otherwise => the composition range is transformed (OT-style).
 *      A pure insertion exactly at `start` or `end` is NOT a conflict; the
 *      inserted text stays outside the composition range (insertion at the
 *      start boundary pushes the range forward; at the end boundary it does
 *      not move; a point composition moves forward as a whole).
 *  R6. Starting a composition while one is active cancels the old one.
 *      Losing focus (blur) cancels the active composition.
 *  R7. Undo/redo of an edit overlapping the composition range follows R5
 *      (transform or cancel). Line endings are stored verbatim; LF and CRLF
 *      are both just UTF-16 code units (CRLF counts as 2).
 */

import { clampOffset, clampRange } from './utf16.ts';

export interface Selection {
  anchor: number;
  focus: number;
}

export interface Edit {
  start: number;
  end: number;
  text: string;
}

export type HistoryKind = 'local' | 'remote' | 'commit';

interface HistoryEntry {
  start: number;
  end: number;
  text: string;
  replaced: string;
  selBefore: Selection;
  selAfter: Selection;
  kind: HistoryKind;
}

export interface CompositionSession {
  /** Generation id; unique per compositionStart. */
  id: number;
  /** Range in FORMAL document coordinates; transformed by external edits. */
  start: number;
  end: number;
  /** Formal text that occupied [start, end) when the composition began. */
  original: string;
  originalSelection: Selection;
  /** Current in-progress composition text (projection lane only). */
  projection: string;
}

function caret(at: number): Selection {
  return { anchor: at, focus: at };
}

/**
 * Transform a composition boundary across an applied edit [s, e) -> `ins` units.
 * Deterministic tie-break for insertions exactly ON a boundary (R5): text
 * inserted at the composition START stays before the composition (the start
 * boundary moves with the content); text inserted at the composition END
 * stays after it (the end boundary does not move).
 */
function transformStart(p: number, s: number, e: number, ins: number): number {
  if (p < s) return p;
  if (p >= e) return p + (ins - (e - s));
  return s;
}

function transformEnd(p: number, s: number, e: number, ins: number): number {
  if (p <= s) return p;
  if (p > e) return p + (ins - (e - s));
  return s;
}

/** Public conflict rule R5: do [start, end) and [s, e) overlap? */
export function rangesConflict(start: number, end: number, s: number, e: number): boolean {
  return s < end && start < e;
}

export class Editor {
  private formal: string;
  private selection: Selection;
  private undoStack: HistoryEntry[] = [];
  private redoStack: HistoryEntry[] = [];
  private session: CompositionSession | null = null;
  private generation = 0;

  constructor(text = '', selection?: Selection) {
    this.formal = text;
    const at = clampOffset(this.formal, selection?.focus ?? 0);
    this.selection = selection
      ? { anchor: clampOffset(this.formal, selection.anchor), focus: at }
      : caret(at);
  }

  // ---- queries -----------------------------------------------------------

  /** Formal (committed) document text. */
  getText(): string {
    return this.formal;
  }

  /** Temporary projection: formal text with the composition range overlaid. */
  getProjection(): string {
    const s = this.session;
    if (!s) return this.formal;
    return this.formal.slice(0, s.start) + s.projection + this.formal.slice(s.end);
  }

  getSelection(): Selection {
    return { ...this.selection };
  }

  isComposing(): boolean {
    return this.session !== null;
  }

  /** Active composition range in formal coordinates, or null. */
  getCompositionRange(): { start: number; end: number } | null {
    const s = this.session;
    return s ? { start: s.start, end: s.end } : null;
  }

  canUndo(): boolean {
    return this.undoStack.length > 0;
  }

  canRedo(): boolean {
    return this.redoStack.length > 0;
  }

  /** Kinds of entries currently on the undo stack (oldest first). For tests. */
  historyKinds(): HistoryKind[] {
    return this.undoStack.map((e) => e.kind);
  }

  setSelection(anchor: number, focus: number): void {
    this.selection = {
      anchor: clampOffset(this.formal, anchor),
      focus: clampOffset(this.formal, focus),
    };
  }

  // ---- composition lifecycle ---------------------------------------------

  /** Begin a composition. Returns the new generation id. */
  compositionStart(): number {
    // R6: an active composition is discarded; the CURRENT selection stays
    // authoritative (unlike an explicit cancel, it is not rewound).
    if (this.session) this.session = null;
    const a = clampOffset(this.formal, Math.min(this.selection.anchor, this.selection.focus));
    const b = clampOffset(this.formal, Math.max(this.selection.anchor, this.selection.focus));
    this.generation += 1;
    this.session = {
      id: this.generation,
      start: a,
      end: b,
      original: this.formal.slice(a, b),
      originalSelection: { ...this.selection },
      projection: '',
    };
    return this.generation;
  }

  /** Update the projection text. Stale generation ids are ignored (R2). */
  compositionUpdate(id: number, text: string): boolean {
    const s = this.session;
    if (!s || s.id !== id) return false;
    s.projection = text;
    return true;
  }

  /**
   * Commit: apply the final text to the formal document as ONE undo unit (R3).
   * A repeated/late commit for a finished generation is a no-op.
   */
  compositionCommit(id: number, text?: string): boolean {
    const s = this.session;
    if (!s || s.id !== id) return false;
    const finalText = text ?? s.projection;
    const { start, end } = s;
    const selBefore = { ...s.originalSelection };
    this.session = null;
    this.applyFormalEdit(
      { start, end, text: finalText },
      'commit',
      selBefore,
      caret(start + finalText.length),
    );
    return true;
  }

  /** Cancel: restore pre-composition text and selection (R4). No undo entry. */
  compositionCancel(id: number): boolean {
    const s = this.session;
    if (!s || s.id !== id) return false;
    this.selection = {
      anchor: clampOffset(this.formal, s.originalSelection.anchor),
      focus: clampOffset(this.formal, s.originalSelection.focus),
    };
    this.session = null;
    return true;
  }

  /** Focus migration cancels the active composition (R6). */
  blur(): void {
    this.session = null;
  }

  // ---- formal edits --------------------------------------------------------

  /** Local (non-composition) edit: one undo unit. */
  applyLocalEdit(edit: Edit): void {
    this.applyExternalEdit(edit, 'local');
  }

  /** Remote edit: one undo unit; conflicts follow R5. */
  applyRemoteEdit(edit: Edit): void {
    this.applyExternalEdit(edit, 'remote');
  }

  private applyExternalEdit(edit: Edit, kind: HistoryKind): void {
    const { start, end } = clampRange(this.formal, edit.start, edit.end);
    const text = edit.text;
    const selAfter = caret(start + text.length);
    const selBefore = { ...this.selection };
    this.applyFormalEdit({ start, end, text }, kind, selBefore, selAfter);
  }

  private applyFormalEdit(
    edit: { start: number; end: number; text: string },
    kind: HistoryKind,
    selBefore: Selection,
    selAfter: Selection,
  ): void {
    const replaced = this.formal.slice(edit.start, edit.end);
    this.formal =
      this.formal.slice(0, edit.start) + edit.text + this.formal.slice(edit.end);
    this.undoStack.push({
      start: edit.start,
      end: edit.end,
      text: edit.text,
      replaced,
      selBefore,
      selAfter,
      kind,
    });
    this.redoStack = [];
    this.selection = {
      anchor: clampOffset(this.formal, selAfter.anchor),
      focus: clampOffset(this.formal, selAfter.focus),
    };
    this.reconcileSession(edit.start, edit.end, edit.text.length);
  }

  /** R5: transform the composition range, or cancel the composition on conflict. */
  private reconcileSession(s: number, e: number, ins: number): void {
    const session = this.session;
    if (!session) return;
    if (rangesConflict(session.start, session.end, s, e)) {
      // External edit wins; composition is cancelled. Caret stays where the
      // external edit left it (set by applyFormalEdit).
      this.session = null;
      return;
    }
    if (session.start === session.end) {
      // Point composition: both boundaries take the start rule so an
      // insertion exactly at the point moves it forward as a whole.
      session.start = transformStart(session.start, s, e, ins);
      session.end = session.start;
    } else {
      session.start = transformStart(session.start, s, e, ins);
      session.end = transformEnd(session.end, s, e, ins);
    }
  }

  // ---- undo / redo ---------------------------------------------------------

  undo(): boolean {
    const entry = this.undoStack.pop();
    if (!entry) return false;
    // Inverse edit: replace [start, start + text.length) with `replaced`.
    this.formal =
      this.formal.slice(0, entry.start) +
      entry.replaced +
      this.formal.slice(entry.start + entry.text.length);
    this.redoStack.push(entry);
    this.selection = {
      anchor: clampOffset(this.formal, entry.selBefore.anchor),
      focus: clampOffset(this.formal, entry.selBefore.focus),
    };
    this.reconcileSession(entry.start, entry.start + entry.text.length, entry.replaced.length);
    return true;
  }

  redo(): boolean {
    const entry = this.redoStack.pop();
    if (!entry) return false;
    this.formal =
      this.formal.slice(0, entry.start) +
      entry.text +
      this.formal.slice(entry.start + entry.replaced.length);
    this.undoStack.push(entry);
    this.selection = {
      anchor: clampOffset(this.formal, entry.selAfter.anchor),
      focus: clampOffset(this.formal, entry.selAfter.focus),
    };
    this.reconcileSession(entry.start, entry.start + entry.replaced.length, entry.text.length);
    return true;
  }
}
