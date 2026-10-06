import { LexState, initialState, cloneState, equalStates } from './state';
import { tokenizeLine, tokensEqual, Token } from './tokenizer';

/** A text replacement over [start, end) in document UTF-16 code-unit offsets. */
export interface Edit {
  start: number;
  end: number;
  text: string;
}

interface LineData {
  text: string;
  /** Input lexical state; null when the line has not been (re)tokenized yet. */
  startState: LexState | null;
  /** Line-relative tokens. */
  tokens: Token[];
  endState: LexState | null;
}

function splitLines(text: string): string[] {
  return text.split('\n');
}

/**
 * Incremental syntax highlighter.
 *
 * Every line stores its input lexical state, its tokens, and its output
 * lexical state. After an edit, lines are retokenized from the first dirty
 * line forward until a line is reached whose recomputed input state AND
 * tokens both match what was already stored; everything past that point is
 * provably unchanged. There is no fixed look-ahead limit: a state change
 * (e.g. a deleted backtick or an unterminated block comment) propagates as
 * far as it needs to, up to the end of the document.
 *
 * All ranges are UTF-16 code-unit offsets (plain JS string indices).
 */
export class IncrementalHighlighter {
  private lines: LineData[] = [];
  private lineStarts: number[] = [];
  /** Number of lines retokenized by the most recent edit (instrumentation). */
  public lastRetokenized = 0;

  constructor(text: string) {
    const normalized = text.replace(/\r\n?/g, '\n');
    this.lines = splitLines(normalized).map((lineText) => ({
      text: lineText,
      startState: null,
      tokens: [],
      endState: null,
    }));
    this.recomputeLineStarts(0);
    this.retokenizeFrom(0);
  }

  get lineCount(): number {
    return this.lines.length;
  }

  /** Input lexical state of a line (a defensive copy). */
  getLineState(line: number): LexState {
    const state = this.lines[line].startState;
    return state ? cloneState(state) : initialState();
  }

  /** Line-relative tokens of a line (copies). */
  getLineTokens(line: number): Token[] {
    return this.lines[line].tokens.map((t) => ({ ...t }));
  }

  /** All tokens in the document with absolute UTF-16 ranges. */
  getTokens(): Token[] {
    const out: Token[] = [];
    for (let i = 0; i < this.lines.length; i++) {
      const base = this.lineStarts[i];
      for (const t of this.lines[i].tokens) {
        out.push({ start: base + t.start, end: base + t.end, type: t.type });
      }
    }
    return out;
  }

  getText(): string {
    return this.lines.map((l) => l.text).join('\n');
  }

  /** Apply an edit; offsets are document UTF-16 code-unit offsets. */
  applyEdit(edit: Edit): void {
    const insertedText = edit.text.replace(/\r\n?/g, '\n');
    const startPos = this.offsetToPosition(edit.start);
    const endPos = this.offsetToPosition(edit.end);

    const before = this.lines[startPos.line].text.slice(0, startPos.col);
    const after = this.lines[endPos.line].text.slice(endPos.col);
    const inserted = splitLines(insertedText);

    let newLineTexts: string[];
    if (inserted.length === 1) {
      newLineTexts = [before + inserted[0] + after];
    } else {
      newLineTexts = [before + inserted[0]];
      for (let i = 1; i < inserted.length - 1; i++) newLineTexts.push(inserted[i]);
      newLineTexts.push(inserted[inserted.length - 1] + after);
    }

    const fresh: LineData[] = newLineTexts.map((lineText) => ({
      text: lineText,
      startState: null,
      tokens: [],
      endState: null,
    }));
    this.lines.splice(startPos.line, endPos.line - startPos.line + 1, ...fresh);

    this.recomputeLineStarts(startPos.line);
    this.retokenizeFrom(startPos.line);
  }

  private offsetToPosition(offset: number): { line: number; col: number } {
    let lo = 0;
    let hi = this.lineStarts.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (this.lineStarts[mid] <= offset) lo = mid;
      else hi = mid - 1;
    }
    return { line: lo, col: offset - this.lineStarts[lo] };
  }

  private recomputeLineStarts(fromLine: number): void {
    let offset = fromLine === 0 ? 0 : this.lineStarts[fromLine - 1] + this.lines[fromLine - 1].text.length + 1;
    for (let i = fromLine; i < this.lines.length; i++) {
      this.lineStarts[i] = offset;
      offset += this.lines[i].text.length + 1; // +1 for the '\n' separator
    }
    this.lineStarts.length = this.lines.length;
  }

  private retokenizeFrom(startLine: number): void {
    this.lastRetokenized = 0;
    let state: LexState =
      startLine === 0 ? initialState() : (this.lines[startLine - 1].endState as LexState);

    for (let i = startLine; i < this.lines.length; i++) {
      const line = this.lines[i];
      const { tokens, endState } = tokenizeLine(line.text, state);
      // Stop only when BOTH the input state and the produced tokens match
      // what is already stored: tokenization is deterministic, so the output
      // state and every downstream line are then guaranteed to be unchanged.
      if (
        line.startState !== null &&
        equalStates(line.startState, state) &&
        tokensEqual(line.tokens, tokens)
      ) {
        break;
      }
      line.startState = cloneState(state);
      line.tokens = tokens;
      line.endState = endState;
      this.lastRetokenized++;
      state = endState;
    }
  }
}
