export interface Token {
  type: 'number' | 'string' | 'identifier' | 'punct' | 'eof';
  value: string;
  line: number;
}

export class LexError extends Error {}

const MULTI_PUNCT = ['===', '!==', '==', '!=', '<=', '>=', '&&', '||'];
const SINGLE_PUNCT = new Set('+-*/%(){};,=<>!:|[]'.split(''));

export function tokenize(source: string): Token[] {
  const tokens: Token[] = [];
  let pos = 0;
  let line = 1;

  const error = (msg: string): never => {
    throw new LexError(`line ${line}: ${msg}`);
  };

  while (pos < source.length) {
    const ch = source[pos];

    if (ch === '\n') {
      line++;
      pos++;
      continue;
    }
    if (ch === ' ' || ch === '\t' || ch === '\r') {
      pos++;
      continue;
    }
    if (ch === '/' && source[pos + 1] === '/') {
      while (pos < source.length && source[pos] !== '\n') pos++;
      continue;
    }
    if (ch === '/' && source[pos + 1] === '*') {
      pos += 2;
      while (pos < source.length && !(source[pos] === '*' && source[pos + 1] === '/')) {
        if (source[pos] === '\n') line++;
        pos++;
      }
      if (pos >= source.length) error('unterminated block comment');
      pos += 2;
      continue;
    }

    if (/[0-9]/.test(ch)) {
      const start = pos;
      while (pos < source.length && /[0-9]/.test(source[pos])) pos++;
      if (source[pos] === '.' && /[0-9]/.test(source[pos + 1] ?? '')) {
        pos++;
        while (pos < source.length && /[0-9]/.test(source[pos])) pos++;
      }
      tokens.push({ type: 'number', value: source.slice(start, pos), line });
      continue;
    }

    if (/[A-Za-z_$]/.test(ch)) {
      const start = pos;
      while (pos < source.length && /[A-Za-z0-9_$]/.test(source[pos])) pos++;
      tokens.push({ type: 'identifier', value: source.slice(start, pos), line });
      continue;
    }

    if (ch === '"' || ch === "'") {
      const quote = ch;
      pos++;
      let value = '';
      while (pos < source.length && source[pos] !== quote) {
        let c = source[pos];
        if (c === '\n') error('unterminated string literal');
        if (c === '\\') {
          pos++;
          const esc = source[pos];
          if (esc === 'n') c = '\n';
          else if (esc === 't') c = '\t';
          else if (esc === 'r') c = '\r';
          else if (esc === '\\') c = '\\';
          else if (esc === "'") c = "'";
          else if (esc === '"') c = '"';
          else error(`unknown escape '\\${esc}'`);
        }
        value += c;
        pos++;
      }
      if (pos >= source.length) error('unterminated string literal');
      pos++;
      tokens.push({ type: 'string', value, line });
      continue;
    }

    const three = source.slice(pos, pos + 3);
    const two = source.slice(pos, pos + 2);
    if (MULTI_PUNCT.includes(three)) {
      tokens.push({ type: 'punct', value: three, line });
      pos += 3;
      continue;
    }
    if (MULTI_PUNCT.includes(two)) {
      tokens.push({ type: 'punct', value: two, line });
      pos += 2;
      continue;
    }
    if (SINGLE_PUNCT.has(ch)) {
      tokens.push({ type: 'punct', value: ch, line });
      pos++;
      continue;
    }

    error(`unexpected character '${ch}'`);
  }

  tokens.push({ type: 'eof', value: '<eof>', line });
  return tokens;
}
