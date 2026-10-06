/**
 * Lexical state carried between lines. All offsets in this library are
 * UTF-16 code-unit offsets (i.e. plain JavaScript string indices).
 */

export type Context =
  | { type: 'blockComment' }
  | { type: 'template' }
  | { type: 'templateExpr'; braceDepth: number }
  | { type: 'continuedString'; quote: string };

export interface LexState {
  /** Stack of open constructs; empty means ordinary top-level code. */
  contexts: Context[];
}

export function initialState(): LexState {
  return { contexts: [] };
}

export function cloneState(state: LexState): LexState {
  return { contexts: state.contexts.map((c) => ({ ...c })) };
}

export function equalStates(a: LexState, b: LexState): boolean {
  if (a.contexts.length !== b.contexts.length) return false;
  for (let i = 0; i < a.contexts.length; i++) {
    const x = a.contexts[i];
    const y = b.contexts[i];
    if (x.type !== y.type) return false;
    if (
      x.type === 'templateExpr' &&
      y.type === 'templateExpr' &&
      x.braceDepth !== y.braceDepth
    ) {
      return false;
    }
    if (
      x.type === 'continuedString' &&
      y.type === 'continuedString' &&
      x.quote !== y.quote
    ) {
      return false;
    }
  }
  return true;
}
