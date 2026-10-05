/**
 * Character-level lexer with persistent state across lines.
 *
 * Directives are only recognized at the start of a line while the lexer is in
 * top-level "code" state, so `#if`-like text inside strings, line comments or
 * block comments (even spanning multiple lines) is never treated as a
 * directive. This is a real state machine, not line-by-line regex matching.
 */

export type LexerState =
  | { kind: 'code'; templateBraceDepth: number }
  | { kind: 'line-comment' }
  | { kind: 'block-comment' }
  | { kind: 'string'; quote: "'" | '"' | '`' };

export class Lexer {
  private stack: LexerState[] = [{ kind: 'code', templateBraceDepth: 0 }];

  /** True when the next line starts in plain code (directives are recognized). */
  get inCode(): boolean {
    return this.stack.length === 1 && this.stack[0].kind === 'code';
  }

  private get top(): LexerState {
    return this.stack[this.stack.length - 1];
  }

  /** Feeds one physical line (without its line terminator) into the machine. */
  advanceLine(line: string): void {
    let i = 0;
    while (i < line.length) {
      const state = this.top;
      const ch = line[i];
      const next = line[i + 1];
      switch (state.kind) {
        case 'code': {
          if (ch === '/' && next === '/') {
            this.stack.push({ kind: 'line-comment' });
            i += 2;
            continue;
          }
          if (ch === '/' && next === '*') {
            this.stack.push({ kind: 'block-comment' });
            i += 2;
            continue;
          }
          if (ch === "'" || ch === '"' || ch === '`') {
            this.stack.push({ kind: 'string', quote: ch });
            i++;
            continue;
          }
          if (this.stack.length > 1) {
            // Inside a template-literal ${ ... } substitution.
            if (ch === '{') {
              state.templateBraceDepth++;
            } else if (ch === '}') {
              if (state.templateBraceDepth === 0) {
                this.stack.pop();
              } else {
                state.templateBraceDepth--;
              }
            }
          }
          i++;
          continue;
        }
        case 'line-comment': {
          i++;
          continue;
        }
        case 'block-comment': {
          if (ch === '*' && next === '/') {
            this.stack.pop();
            i += 2;
            continue;
          }
          i++;
          continue;
        }
        case 'string': {
          if (ch === '\\') {
            i += 2;
            continue;
          }
          if (ch === state.quote) {
            this.stack.pop();
            i++;
            continue;
          }
          if (state.quote === '`' && ch === '$' && next === '{') {
            this.stack.push({ kind: 'code', templateBraceDepth: 0 });
            i += 2;
            continue;
          }
          i++;
          continue;
        }
      }
    }
    // Line comments never carry over to the next line.
    while (this.top.kind === 'line-comment') {
      this.stack.pop();
    }
  }
}
