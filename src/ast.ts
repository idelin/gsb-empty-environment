/**
 * AST and parser for the supported regex subset:
 *
 *   alt    := concat ('|' concat)*        -- union (empty branches allowed)
 *   concat := repeat*                     -- concatenation (possibly empty)
 *   repeat := atom '*'*                   -- Kleene star (may stack: a**)
 *   atom   := '(' alt ')' | '\' any | literal
 *
 * Capturing groups are numbered 1..n in order of their opening parenthesis.
 */

export type Node =
  | { readonly kind: 'empty' }
  | { readonly kind: 'char'; readonly ch: string }
  | { readonly kind: 'concat'; readonly parts: readonly Node[] }
  | { readonly kind: 'alt'; readonly branches: readonly Node[] }
  | { readonly kind: 'star'; readonly child: Node }
  | { readonly kind: 'group'; readonly index: number; readonly child: Node };

export interface Parsed {
  root: Node;
  groupCount: number;
}

export function parse(pattern: string): Parsed {
  let pos = 0;
  let groupCount = 0;

  function peek(): string | undefined {
    return pos < pattern.length ? pattern[pos] : undefined;
  }

  function fail(msg: string): Error {
    return new Error(`regex parse error at offset ${pos}: ${msg} (pattern: ${JSON.stringify(pattern)})`);
  }

  function parseAlt(): Node {
    const branches: Node[] = [parseConcat()];
    while (peek() === '|') {
      pos++;
      branches.push(parseConcat());
    }
    return branches.length === 1 ? branches[0] : { kind: 'alt', branches };
  }

  function parseConcat(): Node {
    const parts: Node[] = [];
    while (pos < pattern.length && peek() !== ')' && peek() !== '|') {
      parts.push(parseRepeat());
    }
    if (parts.length === 0) return { kind: 'empty' };
    if (parts.length === 1) return parts[0];
    return { kind: 'concat', parts };
  }

  function parseRepeat(): Node {
    let node = parseAtom();
    while (peek() === '*') {
      pos++;
      node = { kind: 'star', child: node };
    }
    return node;
  }

  function parseAtom(): Node {
    const ch = peek();
    if (ch === undefined) throw fail('unexpected end of pattern');
    if (ch === '(') {
      pos++;
      const index = ++groupCount;
      const child = parseAlt();
      if (peek() !== ')') throw fail("expected ')'");
      pos++;
      return { kind: 'group', index, child };
    }
    if (ch === ')') throw fail("unmatched ')'");
    if (ch === '*') throw fail("'*' has no preceding atom");
    if (ch === '\\') {
      pos++;
      const esc = peek();
      if (esc === undefined) throw fail('trailing backslash');
      pos++;
      return { kind: 'char', ch: esc };
    }
    pos++;
    return { kind: 'char', ch };
  }

  const root = parseAlt();
  if (pos !== pattern.length) throw fail("unexpected trailing input (unmatched ')'?)");
  return { root, groupCount };
}
