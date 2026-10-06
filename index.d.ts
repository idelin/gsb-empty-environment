export type TokenType =
  | 'identifier'
  | 'number'
  | 'string'
  | 'lineComment'
  | 'blockComment'
  | 'whitespace'
  | 'punct';

export interface Token {
  type: TokenType;
  /** absolute UTF-16 offset, inclusive */
  start: number;
  /** absolute UTF-16 offset, exclusive */
  end: number;
  text: string;
}

export type Diagnostic =
  | { kind: 'unterminated-string'; start: number }
  | { kind: 'unterminated-comment'; start: number; opens: number[] };

export type Mode = 'normal' | 'string' | 'lineComment' | 'blockComment';

export interface Checkpoint {
  version: 1;
  /** absolute source offset of the next fed char */
  offset: number;
  mode: Mode;
  /** start of the pending (unfinished) token */
  tokenStart: number;
  pendingType: TokenType | null;
  pendingText: string;
  escaped: boolean;
  starPending: boolean;
  /** absolute offsets of every unclosed nested block-comment opener */
  commentStack: number[];
  buffer: string;
}

export class Lexer {
  constructor(options?: { offset?: number });
  readonly tokens: Token[];
  readonly diagnostics: Diagnostic[];
  readonly mode: Mode;
  /** Feed a chunk split at any UTF-16 boundary. */
  feed(chunk: string): void;
  /** Finish input; reports unterminated constructs with their start offsets. */
  end(): Diagnostic[];
  /** Serializable pause checkpoint. */
  checkpoint(): Checkpoint;
  /** Resume from a checkpoint; equals continuous scanning. */
  static restore(checkpoint: Checkpoint): Lexer;
}

export class IncrementalLexer {
  constructor(text?: string);
  readonly text: string;
  readonly tokens: Token[];
  readonly diagnostics: Diagnostic[];
  readonly lastRescan: { from: number; scanned: number; stable: boolean } | null;
  setText(text: string): void;
  /**
   * Replace `text[start:end]` with `inserted`, where `start`/`end` are
   * coordinates in the previous version of the document. Rescans from the
   * token boundary at or before the edit until lexical state and a token
   * boundary are both stable.
   */
  applyEdit(start: number, end: number, inserted: string): void;
}
