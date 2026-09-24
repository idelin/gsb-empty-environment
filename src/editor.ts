/**
 * Composition editing state machine.
 *
 * Two states of truth are maintained while composing:
 *   - FORMAL VERSION: the committed document. Only this version enters the
 *     undo history and only this version is transformed by remote edits.
 *   - TEMPORARY PROJECTION: what the user sees = formal text with the
 *     composition range replaced by the current composed text.
 *
 * State machine (per composition session, each with a unique generation id):
 *
 *      start         update*          commit            cancel
 *   idle -----> composing -----> composing -----> idle   -----> idle
 *                  ^   |              (1 undo unit)   (0 undo units)
 *                  +---+
 *
 *   External edit while composing:
 *     - non-conflicting -> transform composition range, stay `composing`
 *     - conflicting     -> end composition (policy `onConflict`, default
 *                          cancel), then apply the edit
 *   undo/redo while composing -> cancel composition first, then undo/redo
 *   blur while composing      -> policy `onBlur` (default commit)
 *   start while composing     -> commit current session, open a new one
 *
 * Generation rule: every session has a monotonically increasing id. Any
 * update/commit/cancel carrying a stale or already-settled id is ignored,
 * so late or duplicated callbacks can never pollute a newer composition or
 * insert text twice.
 */

import {
  applyEdit,
  editsConflict,
  spliceRange,
  transformPoint,
  transformRange,
  type Edit,
  type Range,
} from './rules.ts';
import { snapRange } from './utf16.ts';
import { History, type Selection } from './history.ts';

export type Phase = 'idle' | 'composing';

export type CancelReason =
  | 'explicit'
  | 'conflict'
  | 'undo'
  | 'redo'
  | 'blur'
  | 'superseded';

export type ConflictPolicy = 'cancel' | 'commit';
export type BlurPolicy = 'commit' | 'cancel';

export type EventResult =
  | 'applied'
  | 'ignored-no-session'
  | 'ignored-stale-generation'
  | 'ignored-duplicate';

export interface EditorOptions {
  text?: string;
  onConflict?: ConflictPolicy;
  onBlur?: BlurPolicy;
}

export interface CompositionSessionInfo {
  id: number;
  range: Range;
  text: string;
  updates: number;
  state: 'active' | 'committed' | 'cancelled';
}

export interface CompositionHandle {
  readonly id: number;
  update(text: string): EventResult;
  commit(): EventResult;
  cancel(): EventResult;
}

interface Session {
  id: number;
  range: Range;
  text: string;
  savedSelection: Selection;
  updates: number;
  state: 'active' | 'committed' | 'cancelled';
}

export interface EditorStats {
  commitsByGeneration: Map<number, number>;
  cancelsByReason: Map<CancelReason, number>;
  staleIgnored: number;
  duplicateIgnored: number;
  noSessionIgnored: number;
  remoteEditsApplied: number;
  conflictCancels: number;
}

export class Editor {
  private formal: string;
  private selection: Selection;
  private readonly history = new History();
  private session: Session | null = null;
  private nextGeneration = 1;
  private readonly settledGenerations = new Set<number>();
  private readonly onConflict: ConflictPolicy;
  private readonly onBlur: BlurPolicy;

  readonly stats: EditorStats = {
    commitsByGeneration: new Map(),
    cancelsByReason: new Map(),
    staleIgnored: 0,
    duplicateIgnored: 0,
    noSessionIgnored: 0,
    remoteEditsApplied: 0,
    conflictCancels: 0,
  };

  constructor(options: EditorOptions = {}) {
    this.formal = options.text ?? '';
    this.selection = { anchor: 0, head: 0 };
    this.onConflict = options.onConflict ?? 'cancel';
    this.onBlur = options.onBlur ?? 'commit';
  }

  // ---------------------------------------------------------------- views

  get phase(): Phase {
    return this.session === null ? 'idle' : 'composing';
  }

  /** The formal (committed) document. */
  getFormalText(): string {
    return this.formal;
  }

  /** The temporary projection the user sees. */
  getProjection(): string {
    const session = this.session;
    if (!session) return this.formal;
    return spliceRange(this.formal, session.range, session.text);
  }

  /** Selection in projection coordinates. */
  getSelection(): Selection {
    const session = this.session;
    if (!session) return { ...this.selection };
    const caret = session.range.start + session.text.length;
    return { anchor: caret, head: caret };
  }

  get undoDepth(): number {
    return this.history.undoDepth;
  }

  get redoDepth(): number {
    return this.history.redoDepth;
  }

  historySnapshots(): string[] {
    return this.history.snapshots();
  }

  currentComposition(): CompositionSessionInfo | null {
    if (!this.session) return null;
    const { id, range, text, updates, state } = this.session;
    return { id, range: { ...range }, text, updates, state };
  }

  // ------------------------------------------------------------- selection

  setSelection(anchor: number, head: number = anchor): void {
    const base = this.session ? this.getProjection() : this.formal;
    const a = snapRange(base, anchor, anchor).start;
    const h = snapRange(base, head, head).start;
    if (this.session) {
      // Selections are stored in formal coordinates while composing.
      this.selection = { anchor: a, head: h };
    } else {
      this.selection = { anchor: a, head: h };
    }
  }

  // ----------------------------------------------------------- composition

  /** Begin a composition session. Implicitly commits any active one. */
  compositionStart(): CompositionHandle {
    if (this.session) this.endSession('commit', 'superseded');
    const id = this.nextGeneration++;
    const { start, end } = snapRange(
      this.formal,
      Math.min(this.selection.anchor, this.selection.head),
      Math.max(this.selection.anchor, this.selection.head),
    );
    const session: Session = {
      id,
      range: { start, end },
      text: '',
      savedSelection: { ...this.selection },
      updates: 0,
      state: 'active',
    };
    this.session = session;
    const self = this;
    return {
      id,
      update(text: string): EventResult {
        return self.updateComposition(text, id);
      },
      commit(): EventResult {
        return self.commitComposition(id);
      },
      cancel(): EventResult {
        return self.cancelComposition(id, 'explicit');
      },
    };
  }

  /** Replace the composed text of the active session (projection only). */
  updateComposition(text: string, generation: number): EventResult {
    const session = this.session;
    if (!session) return this.ignoreNoSession(generation);
    if (session.id !== generation) return this.ignoreStale();
    session.text = text;
    session.updates += 1;
    return 'applied';
  }

  /** Commit the active session: exactly one formal edit, one undo unit. */
  commitComposition(generation: number): EventResult {
    const session = this.session;
    if (!session) return this.ignoreNoSession(generation);
    if (session.id !== generation) return this.ignoreStale();
    this.endSession('commit', 'explicit');
    return 'applied';
  }

  /** Cancel the active session: formal text is left untouched. */
  cancelComposition(generation: number, reason: CancelReason = 'explicit'): EventResult {
    const session = this.session;
    if (!session) return this.ignoreNoSession(generation);
    if (session.id !== generation) return this.ignoreStale();
    this.endSession('cancel', reason);
    return 'applied';
  }

  // ------------------------------------------------------- external edits

  /**
   * Apply a remote edit. Remote edits never create undo units; they are
   * rebased through the history so local undo cannot erase remote content.
   * Returns how the composition (if any) was affected.
   */
  applyRemoteEdit(edit: Edit): 'applied' | 'transformed-composition' | 'ended-composition' {
    const snapped = this.snapToFormal(edit);
    const outcome = this.applyExternalEdit(snapped, false);
    this.stats.remoteEditsApplied += 1;
    return outcome;
  }

  /**
   * Apply a local (non-composition) edit. Recorded as its own undo unit.
   * Goes through the same conflict rules as remote edits.
   */
  applyLocalEdit(edit: Edit, label = 'local-edit'): 'applied' | 'transformed-composition' | 'ended-composition' {
    const snapped = this.snapToFormal(edit);
    return this.applyExternalEdit(snapped, true, label);
  }

  private applyExternalEdit(
    edit: Edit,
    recordUnit: boolean,
    label = 'local-edit',
  ): 'applied' | 'transformed-composition' | 'ended-composition' {
    let outcome: 'applied' | 'transformed-composition' | 'ended-composition' = 'applied';
    const session = this.session;
    if (session) {
      if (editsConflict(session.range, edit)) {
        this.endSession(this.onConflict === 'cancel' ? 'cancel' : 'commit', 'conflict');
        outcome = 'ended-composition';
      } else {
        session.range = transformRange(session.range, edit);
        session.savedSelection = {
          anchor: transformPoint(session.savedSelection.anchor, edit),
          head: transformPoint(session.savedSelection.head, edit),
        };
        outcome = 'transformed-composition';
      }
    }
    const before = this.formal;
    const selectionBefore = { ...this.selection };
    this.formal = applyEdit(this.formal, edit);
    this.selection = {
      anchor: transformPoint(this.selection.anchor, edit),
      head: transformPoint(this.selection.head, edit),
    };
    this.history.rebase(edit);
    if (recordUnit && this.formal !== before) {
      this.history.record({
        label,
        origin: 'local',
        formalBefore: before,
        formalAfter: this.formal,
        selectionBefore,
        selectionAfter: { ...this.selection },
      });
    }
    return outcome;
  }

  // -------------------------------------------------------------- history

  /** Undo. An active composition is cancelled first (never recorded). */
  undo(): boolean {
    if (this.session) this.endSession('cancel', 'undo');
    const unit = this.history.popUndo();
    if (!unit) return false;
    this.formal = unit.formalBefore;
    this.selection = { ...unit.selectionBefore };
    return true;
  }

  /** Redo. An active composition is cancelled first. */
  redo(): boolean {
    if (this.session) this.endSession('cancel', 'redo');
    const unit = this.history.popRedo();
    if (!unit) return false;
    this.formal = unit.formalAfter;
    this.selection = { ...unit.selectionAfter };
    return true;
  }

  // ---------------------------------------------------------------- focus

  /** Focus migration: deterministic policy, default commits the session. */
  blur(): void {
    if (!this.session) return;
    this.endSession(this.onBlur === 'commit' ? 'commit' : 'cancel', 'blur');
  }

  focus(): void {
    // No state change; provided so focus/blur interleavings are explicit.
  }

  // -------------------------------------------------------------- internals

  private endSession(kind: 'commit' | 'cancel', reason: CancelReason): void {
    const session = this.session;
    if (!session) return;
    if (kind === 'commit') {
      const before = this.formal;
      const selectionBefore = { ...this.selection };
      const after = spliceRange(this.formal, session.range, session.text);
      this.formal = after;
      const caret = session.range.start + session.text.length;
      this.selection = { anchor: caret, head: caret };
      session.state = 'committed';
      this.stats.commitsByGeneration.set(
        session.id,
        (this.stats.commitsByGeneration.get(session.id) ?? 0) + 1,
      );
      if (after !== before) {
        this.history.record({
          label: 'composition',
          origin: 'composition-commit',
          formalBefore: before,
          formalAfter: after,
          selectionBefore,
          selectionAfter: { ...this.selection },
        });
      }
    } else {
      session.state = 'cancelled';
      this.selection = { ...session.savedSelection };
      this.stats.cancelsByReason.set(
        reason,
        (this.stats.cancelsByReason.get(reason) ?? 0) + 1,
      );
      if (reason === 'conflict') this.stats.conflictCancels += 1;
    }
    this.settledGenerations.add(session.id);
    this.session = null;
  }

  private ignoreNoSession(generation: number): EventResult {
    if (this.settledGenerations.has(generation)) {
      this.stats.duplicateIgnored += 1;
      return 'ignored-duplicate';
    }
    this.stats.noSessionIgnored += 1;
    return 'ignored-no-session';
  }

  private ignoreStale(): EventResult {
    this.stats.staleIgnored += 1;
    return 'ignored-stale-generation';
  }

  private snapToFormal(edit: Edit): Edit {
    const { start, end } = snapRange(this.formal, edit.start, edit.end);
    return { start, end, text: edit.text };
  }
}
