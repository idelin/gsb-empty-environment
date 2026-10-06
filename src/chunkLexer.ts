import {
  Scanner,
  ScannerState,
  Token,
  Unterminated,
  cloneState,
} from './scanner';

/**
 * Streaming lexer fed with arbitrary string chunks. Chunks may be split at
 * any UTF-16 boundary (even inside a surrogate pair is fine as long as the
 * pair is delivered in order across two chunks — offsets are code units).
 */
export class ChunkLexer {
  private scanner: Scanner;
  readonly tokens: Token[] = [];

  constructor(state?: ScannerState) {
    this.scanner = new Scanner(state);
  }

  /** Feed the next chunk. Returns the tokens completed by this chunk. */
  feed(chunk: string): Token[] {
    const before = this.tokens.length;
    this.scanner.scan(chunk, this.scanner.state.offset, (t) => this.tokens.push(t));
    return this.tokens.slice(before);
  }

  /**
   * Pause checkpoint: a plain JSON-serializable object capturing the
   * unfinished string, the nested-comment stack and the source offset.
   * `ChunkLexer.resume(cp)` continues exactly where this lexer left off.
   */
  checkpoint(): ScannerState {
    return cloneState(this.scanner.state);
  }

  /** Build a lexer that resumes from a previously captured checkpoint. */
  static resume(cp: ScannerState): ChunkLexer {
    return new ChunkLexer(cloneState(cp));
  }

  /** Absolute offset of the next unconsumed character. */
  get offset(): number {
    return this.scanner.state.offset;
  }

  /**
   * Signal end of input. Flushes the pending token and returns the
   * unterminated constructs (their absolute start offsets).
   */
  finish(): Unterminated[] {
    return this.scanner.finish((t) => this.tokens.push(t));
  }
}

/** One-shot helper: lex a whole string. */
export function lexAll(text: string): { tokens: Token[]; unterminated: Unterminated[] } {
  const lexer = new ChunkLexer();
  lexer.feed(text);
  const unterminated = lexer.finish();
  return { tokens: lexer.tokens, unterminated };
}
