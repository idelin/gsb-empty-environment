import { TextDocument, type Position } from './document.ts';
import { IncrementalLexer, type Token } from './lexer.ts';
import { analyzeBrackets, type BracketAnalysis, type BracketPair, type Diagnostic } from './brackets.ts';

/**
 * Facade over the document, the incremental lexer and the bracket analyzer.
 * All public offsets are UTF-16 code-unit offsets; edits that would split a
 * surrogate pair are rejected with a RangeError.
 */
export class LanguageService {
  private readonly doc: TextDocument;
  private readonly lexer: IncrementalLexer;
  private analysis: BracketAnalysis;

  constructor(text = '') {
    this.doc = new TextDocument(text);
    this.lexer = new IncrementalLexer(text);
    this.analysis = analyzeBrackets(this.lexer.getTokens());
  }

  get text(): string {
    return this.doc.text;
  }

  get lineCount(): number {
    return this.doc.lineCount;
  }

  /** Replaces the whole document. */
  setText(text: string): void {
    this.doc.applyEdit(0, this.doc.text.length, text);
    this.lexer.setText(text);
    this.analysis = analyzeBrackets(this.lexer.getTokens());
  }

  /**
   * Replaces [start, end) with `newText` and re-analyzes. Re-lexing is
   * incremental: only the affected region plus the tokens needed to
   * resynchronize are scanned again.
   */
  applyEdit(start: number, end: number, newText: string): void {
    this.doc.applyEdit(start, end, newText); // validates UTF-16 boundaries first
    this.lexer.applyEdit(this.doc.text, start, end, newText.length);
    this.analysis = analyzeBrackets(this.lexer.getTokens());
  }

  positionAt(offset: number): Position {
    return this.doc.positionAt(offset);
  }

  offsetAt(position: Position): number {
    return this.doc.offsetAt(position);
  }

  getTokens(): readonly Token[] {
    return this.lexer.getTokens();
  }

  getDiagnostics(): readonly Diagnostic[] {
    return this.analysis.diagnostics;
  }

  /** All matched bracket pairs, sorted by opening offset. */
  getPairs(): readonly BracketPair[] {
    return this.analysis.pairs;
  }

  /** Matching bracket offset for the bracket at `offset`, or null. */
  matchAt(offset: number): number | null {
    return this.analysis.matchAt(offset);
  }

  /** The pair the bracket at `offset` belongs to, or null. */
  pairAt(offset: number): BracketPair | null {
    return this.analysis.pairAt(offset);
  }

  /** Innermost matched pair strictly enclosing `offset`, or null. */
  enclosingPair(offset: number): BracketPair | null {
    return this.analysis.enclosingPair(offset);
  }
}
