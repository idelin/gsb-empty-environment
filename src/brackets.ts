import { MATCH_CLOSE, type Token } from './lexer.ts';

export interface BracketPair {
  /** UTF-16 offset of the opening bracket. */
  open: number;
  /** UTF-16 offset of the closing bracket. */
  close: number;
  openChar: string;
  closeChar: string;
  /** 1-based nesting depth. */
  depth: number;
}

export type DiagnosticCode =
  | 'unmatched-close'
  | 'mismatched-close'
  | 'unclosed-open'
  | 'unterminated-string'
  | 'unterminated-block-comment';

export interface Diagnostic {
  code: DiagnosticCode;
  /** UTF-16 offsets, [start, end). */
  start: number;
  end: number;
  message: string;
}

export interface BracketAnalysis {
  /** All matched pairs, sorted by opening offset. */
  readonly pairs: readonly BracketPair[];
  readonly diagnostics: readonly Diagnostic[];
  /** Returns the matching bracket offset for the bracket at `offset`, or null. */
  matchAt(offset: number): number | null;
  /** Returns the pair that the bracket bracket at `offset` belongs to, or null. */
  pairAt(offset: number): BracketPair | null;
  /**
   * Returns the innermost matched pair strictly enclosing `offset`
   * (open < offset < close), or null when there is none.
   */
  enclosingPair(offset: number): BracketPair | null;
}

/**
 * Matches brackets over the token stream with an explicit stack (iterative,
 * safe for nesting depths in the tens of thousands) and derives diagnostics.
 *
 * Recovery rule for a closer that does not match the innermost opener: the
 * closer is reported as `mismatched-close` and the stack is left untouched,
 * so later brackets are still matched against the same openers.
 */
export function analyzeBrackets(tokens: readonly Token[]): BracketAnalysis {
  const diagnostics: Diagnostic[] = [];
  const matchOf = new Map<number, number>();
  const matched: Array<{ open: number; close: number; openChar: string; closeChar: string }> = [];
  const stack: Array<{ char: string; offset: number }> = [];

  for (const token of tokens) {
    if (token.unterminated && token.type === 'string') {
      diagnostics.push({
        code: 'unterminated-string',
        start: token.start,
        end: token.end,
        message: 'Unterminated string literal',
      });
      continue;
    }
    if (token.unterminated && token.type === 'block-comment') {
      diagnostics.push({
        code: 'unterminated-block-comment',
        start: token.start,
        end: token.end,
        message: 'Unterminated block comment',
      });
      continue;
    }
    if (token.type === 'open') {
      stack.push({ char: token.char!, offset: token.start });
    } else if (token.type === 'close') {
      const top = stack[stack.length - 1];
      if (top === undefined) {
        diagnostics.push({
          code: 'unmatched-close',
          start: token.start,
          end: token.end,
          message: `Closing bracket '${token.char}' has no matching opener`,
        });
      } else if (MATCH_CLOSE[top.char] === token.char) {
        stack.pop();
        matched.push({ open: top.offset, close: token.start, openChar: top.char, closeChar: token.char! });
        matchOf.set(top.offset, token.start);
        matchOf.set(token.start, top.offset);
      } else {
        diagnostics.push({
          code: 'mismatched-close',
          start: token.start,
          end: token.end,
          message:
            `Expected '${MATCH_CLOSE[top.char]}' to close '${top.char}' ` +
            `at offset ${top.offset}, but found '${token.char}'`,
        });
      }
    }
  }
  for (const opener of stack) {
    diagnostics.push({
      code: 'unclosed-open',
      start: opener.offset,
      end: opener.offset + 1,
      message: `Opening bracket '${opener.char}' is never closed`,
    });
  }
  diagnostics.sort((a, b) => a.start - b.start || a.end - b.end);

  // Pairs sorted by opening offset, with depth and parent links computed in
  // one iterative pass (pairs are properly nested by construction).
  const byOpen = [...matched].sort((a, b) => a.open - b.open);
  const pairs: BracketPair[] = byOpen.map((p) => ({ ...p, depth: 0 }));
  const parent: number[] = new Array<number>(pairs.length).fill(-1);
  const chain: number[] = [];
  for (let i = 0; i < pairs.length; i++) {
    while (chain.length > 0 && pairs[chain[chain.length - 1]].close < pairs[i].open) {
      chain.pop();
    }
    if (chain.length > 0) parent[i] = chain[chain.length - 1];
    chain.push(i);
    pairs[i].depth = chain.length;
  }

  const pairIndexAt = new Map<number, number>();
  for (let i = 0; i < pairs.length; i++) {
    pairIndexAt.set(pairs[i].open, i);
    pairIndexAt.set(pairs[i].close, i);
  }

  return {
    pairs,
    diagnostics,
    matchAt(offset: number): number | null {
      const found = matchOf.get(offset);
      return found === undefined ? null : found;
    },
    pairAt(offset: number): BracketPair | null {
      const index = pairIndexAt.get(offset);
      return index === undefined ? null : pairs[index];
    },
    enclosingPair(offset: number): BracketPair | null {
      // Binary search: rightmost pair whose opener is left of `offset`.
      let low = 0;
      let high = pairs.length - 1;
      let candidate = -1;
      while (low <= high) {
        const mid = (low + high) >> 1;
        if (pairs[mid].open < offset) {
          candidate = mid;
          low = mid + 1;
        } else {
          high = mid - 1;
        }
      }
      // Any enclosing pair is an ancestor of that candidate; walk up the
      // parent chain (iteratively) until one actually contains the offset.
      let i = candidate;
      while (i !== -1) {
        if (pairs[i].close > offset) return pairs[i];
        i = parent[i];
      }
      return null;
    },
  };
}
