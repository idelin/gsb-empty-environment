import type { Ast, ParsedPattern } from './types.js';

export function parsePattern(source: string): ParsedPattern {
  let pos = 0;
  let groupCount = 0;

  function fail(message: string): SyntaxError {
    return new SyntaxError(`invalid pattern ${JSON.stringify(source)}: ${message} at offset ${pos}`);
  }

  function peek(): string {
    return pos < source.length ? source[pos] : '';
  }

  function parseAlt(): Ast {
    const options: Ast[] = [parseConcat()];
    while (peek() === '|') {
      pos++;
      options.push(parseConcat());
    }
    return options.length === 1 ? options[0] : { kind: 'alt', options };
  }

  function parseConcat(): Ast {
    const parts: Ast[] = [];
    while (pos < source.length && peek() !== '|' && peek() !== ')') {
      parts.push(parseRepeat());
    }
    return parts.length === 1 ? parts[0] : { kind: 'concat', parts };
  }

  function parseRepeat(): Ast {
    let atom = parseAtom();
    while (peek() === '*') {
      pos++;
      atom = { kind: 'star', inner: atom };
    }
    return atom;
  }

  function parseAtom(): Ast {
    const ch = peek();
    if (ch === '') throw fail('unexpected end of pattern');
    if (ch === '*') throw fail('nothing to repeat');
    if (ch === '|' || ch === ')') throw fail(`unexpected ${JSON.stringify(ch)}`);
    if (ch === '(') {
      pos++;
      groupCount++;
      const index = groupCount;
      const inner = parseAlt();
      if (peek() !== ')') throw fail('unclosed group');
      pos++;
      return { kind: 'group', index, inner };
    }
    if (ch === '\\') {
      pos++;
      const escaped = peek();
      if (escaped === '') throw fail('trailing backslash');
      pos++;
      return { kind: 'lit', ch: escaped };
    }
    pos++;
    return { kind: 'lit', ch };
  }

  const ast = parseAlt();
  if (pos < source.length) throw fail(`unexpected ${JSON.stringify(peek())}`);
  return { ast, groupCount };
}
