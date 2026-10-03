import type { Pos } from './grammar.ts';

/** A language-level diagnostic carrying the precise AST node position. */
export class LangError extends Error {
  readonly pos: Pos;
  constructor(message: string, pos: Pos) {
    super(`${message} (at ${pos.line}:${pos.col})`);
    this.name = 'LangError';
    this.pos = pos;
  }
}
