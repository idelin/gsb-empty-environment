import {
  Scanner,
  ScannerState,
  Token,
  Unterminated,
  cloneState,
  initialState,
} from './scanner';

/** An edit expressed in coordinates of the current (old) version. */
export interface Edit {
  /** Absolute UTF-16 offset where the replaced range starts. */
  start: number;
  /** Absolute UTF-16 offset one past the replaced range. */
  end: number;
  /** Replacement text (may be empty for a deletion). */
  newText: string;
}

/**
 * A resumption point: a true scanner state at some source offset, plus the
 * index of the first token a rescan from here will (re-)emit. For states
 * whose `tokenStart` lies before `offset` (a code run is only finalized by
 * the next construct's opener), resuming re-emits that run, so `tokenIndex`
 * points at the run's own token.
 */
interface Checkpoint {
  state: ScannerState;
  tokenIndex: number;
}

/** Map an old-coordinate offset into new coordinates. Returns null when the
 * offset points inside the replaced range (its content no longer exists). */
function shiftOffset(offset: number, edit: Edit, delta: number): number | null {
  if (offset < edit.start) return offset;
  if (offset >= edit.end) return offset + delta;
  return null;
}

function shiftState(state: ScannerState, edit: Edit, delta: number): ScannerState | null {
  const offset = shiftOffset(state.offset, edit, delta);
  const tokenStart = shiftOffset(state.tokenStart, edit, delta);
  if (offset === null || tokenStart === null) return null;
  const commentStack: number[] = [];
  for (const o of state.commentStack) {
    const shifted = shiftOffset(o, edit, delta);
    if (shifted === null) return null;
    commentStack.push(shifted);
  }
  return { offset, mode: state.mode, tokenStart, escaped: state.escaped, commentStack };
}

function shiftedEquals(oldState: ScannerState, newState: ScannerState, edit: Edit, delta: number): boolean {
  const shifted = shiftState(oldState, edit, delta);
  if (shifted === null) return false;
  if (shifted.offset !== newState.offset || shifted.mode !== newState.mode ||
      shifted.tokenStart !== newState.tokenStart || shifted.escaped !== newState.escaped ||
      shifted.commentStack.length !== newState.commentStack.length) {
    return false;
  }
  for (let i = 0; i < shifted.commentStack.length; i++) {
    if (shifted.commentStack[i] !== newState.commentStack[i]) return false;
  }
  return true;
}

function shiftToken(token: Token, delta: number): Token {
  return { kind: token.kind, start: token.start + delta, end: token.end + delta };
}

/** Unterminated constructs implied by a pre-flush end state. */
function diagnosticsFor(endState: ScannerState): Unterminated[] {
  switch (endState.mode) {
    case 'string':
      return [{ kind: 'string', start: endState.tokenStart }];
    case 'blockComment':
    case 'blockCommentSlash':
    case 'blockCommentStar':
      return endState.commentStack.map((start) => ({ kind: 'blockComment' as const, start }));
    default:
      return [];
  }
}

/**
 * A lexed document supporting incremental edits in old-version coordinates.
 *
 * Re-lexing after an edit resumes from the stored scanner state at the last
 * checkpoint at or before the edit — never a fixed number of lines — and
 * continues until both the lexical state and a token boundary line up with
 * the pre-edit token stream (shifted by the edit delta), at which point the
 * untouched tail is reused.
 */
export class IncrementalDocument {
  private text: string;
  private tokens: Token[] = [];
  private checkpoints: Checkpoint[] = [];
  /** Pre-flush scanner state at end of text. */
  private endState: ScannerState = initialState();
  private diagnostics: Unterminated[] = [];
  /** Characters consumed by the most recent rescan (for observability). */
  private lastRescanChars = 0;

  constructor(text: string) {
    this.text = text;
    this.fullRelex();
  }

  getText(): string {
    return this.text;
  }

  getTokens(): Token[] {
    return this.tokens.map((t) => ({ ...t }));
  }

  getDiagnostics(): Unterminated[] {
    return this.diagnostics.map((d) => ({ ...d }));
  }

  /** Number of characters the last `applyEdit` had to re-scan. */
  getLastRescanChars(): number {
    return this.lastRescanChars;
  }

  private fullRelex(): void {
    this.tokens = [];
    this.checkpoints = [{ state: initialState(0), tokenIndex: 0 }];
    const scanner = new Scanner();
    scanner.scan(this.text, 0, (t, boundary) => {
      const idx = this.tokens.length;
      this.tokens.push(t);
      this.checkpoints.push({
        state: cloneState(boundary),
        tokenIndex: t.kind === 'code' ? idx : idx + 1,
      });
    });
    this.endState = cloneState(scanner.state);
    scanner.finish((t) => this.tokens.push(t));
    this.diagnostics = diagnosticsFor(this.endState);
  }

  /** Apply an edit given in coordinates of the current version. */
  applyEdit(edit: Edit): void {
    const { start, end, newText } = edit;
    if (!(0 <= start && start <= end && end <= this.text.length)) {
      throw new Error(`Invalid edit [${start}, ${end}) for text of length ${this.text.length}`);
    }
    const delta = newText.length - (end - start);
    const nextText = this.text.slice(0, start) + newText + this.text.slice(end);

    // Last checkpoint at or before the edit start; its state is reproducible
    // because everything before `start` is unchanged.
    let ci = 0;
    for (let i = 1; i < this.checkpoints.length; i++) {
      if (this.checkpoints[i].state.offset <= start) ci = i;
      else break;
    }
    const startCp = this.checkpoints[ci];
    const rescanFrom = startCp.state.offset;

    const scanner = new Scanner(startCp.state);
    const mid: Token[] = [];
    const midCps: Checkpoint[] = [];
    let reuseFrom = -1; // old token index whose tail can be reused
    let reuseCp = -1; // index of the old checkpoint that matched

    scanner.scanRange(nextText, scanner.state.offset, nextText.length, (t, boundary) => {
      const idx = startCp.tokenIndex + mid.length;
      mid.push(t);
      midCps.push({
        state: cloneState(boundary),
        tokenIndex: t.kind === 'code' ? idx : idx + 1,
      });
      if (reuseFrom === -1 && boundary.offset >= start + newText.length) {
        // At a fresh token boundary past the edit: look for an old checkpoint
        // beyond the edited range whose shifted state matches exactly.
        for (let k = 0; k < this.checkpoints.length; k++) {
          const old = this.checkpoints[k];
          if (old.state.offset < end) continue;
          if (old.state.offset + delta !== boundary.offset) continue;
          if (shiftedEquals(old.state, boundary, edit, delta)) {
            // The token containing the boundary differs if the boundary
            // state has an open token started before the boundary (a code
            // run): it is re-emitted by the rescan, so reuse starts after it.
            reuseFrom = old.tokenIndex + (old.state.tokenStart < old.state.offset ? 1 : 0);
            reuseCp = k;
            scanner.requestStop();
            break;
          }
        }
      }
    });

    const head = this.tokens.slice(0, startCp.tokenIndex);
    const headCps = this.checkpoints.slice(0, ci + 1);
    let tail: Token[] = [];
    let tailCps: Checkpoint[] = [];
    let newEndState: ScannerState;

    if (reuseFrom >= 0) {
      tail = this.tokens.slice(reuseFrom).map((t) => shiftToken(t, delta));
      const shiftedEnd = shiftState(this.endState, edit, delta);
      let broken = shiftedEnd === null;
      if (!broken) newEndState = shiftedEnd as ScannerState;
      for (let k = reuseCp + 1; k < this.checkpoints.length && !broken; k++) {
        const old = this.checkpoints[k];
        const shifted = shiftState(old.state, edit, delta);
        if (shifted === null) {
          broken = true;
        } else {
          tailCps.push({
            state: shifted,
            tokenIndex: startCp.tokenIndex + mid.length + (old.tokenIndex - reuseFrom),
          });
        }
      }
      if (broken) {
        // Defensive: some reused state referenced edited content; recompute.
        this.text = nextText;
        this.fullRelex();
        return;
      }
      newEndState = shiftedEnd as ScannerState;
      this.lastRescanChars = scanner.state.offset - rescanFrom;
    } else {
      // No stable point found: the rescan ran to end of input.
      newEndState = cloneState(scanner.state);
      scanner.finish((t) => mid.push(t));
      this.lastRescanChars = nextText.length - rescanFrom;
    }

    this.tokens = head.concat(mid, tail);
    this.checkpoints = headCps.concat(midCps, tailCps);
    this.endState = newEndState;
    this.text = nextText;
    this.diagnostics = diagnosticsFor(this.endState);
  }
}
