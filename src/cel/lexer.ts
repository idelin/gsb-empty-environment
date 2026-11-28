export type TokenType =
  | 'IDENT'
  | 'INT'
  | 'UINT'
  | 'DOUBLE'
  | 'STRING'
  | 'BYTES'
  | 'OP'
  | 'EOF';

export interface Token {
  type: TokenType;
  value: string;
  pos: number;
}

const KEYWORDS = new Set([
  'true',
  'false',
  'null',
  'in',
  'as',
  'if',
  'then',
  'else',
  'for',
  'while',
  'break',
  'return',
  'function',
  'let',
  'const',
]);

const THREE_CHAR_OPS = ['===', '!==', '...'];
const TWO_CHAR_OPS = [
  '==',
  '!=',
  '<=',
  '>=',
  '&&',
  '||',
  '//',
  '..',
];
const SINGLE_CHAR_OPS = new Set([
  '.',
  ',',
  '(',
  ')',
  '[',
  ']',
  '{',
  '}',
  '+',
  '-',
  '*',
  '/',
  '%',
  '!',
  '<',
  '>',
  ':',
  '?',
  '=',
]);

export class LexError extends Error {
  constructor(message: string, readonly pos: number) {
    super(`CEL lex error at ${pos}: ${message}`);
    this.name = 'LexError';
  }
}

function isIdentStart(ch: string): boolean {
  return /[A-Za-z_]/.test(ch);
}

function isIdentPart(ch: string): boolean {
  return /[A-Za-z0-9_]/.test(ch);
}

function isDigit(ch: string): boolean {
  return ch >= '0' && ch <= '9';
}

function isHexDigit(ch: string): boolean {
  return /[0-9a-fA-F]/.test(ch);
}

export function tokenize(source: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  const n = source.length;

  const push = (type: TokenType, value: string, pos: number): void => {
    tokens.push({ type, value, pos });
  };

  while (i < n) {
    const ch = source[i];

    if (ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r') {
      i += 1;
      continue;
    }

    const start = i;

    if (isIdentStart(ch)) {
      i += 1;
      while (i < n && isIdentPart(source[i])) i += 1;
      const word = source.slice(start, i);
      if (KEYWORDS.has(word)) {
        push('OP', word, start);
      } else {
        push('IDENT', word, start);
      }
      continue;
    }

    if (isDigit(ch) || (ch === '.' && isDigit(source[i + 1] ?? ''))) {
      let isDouble = false;
      if (ch === '0' && (source[i + 1] === 'x' || source[i + 1] === 'X')) {
        i += 2;
        const hexStart = i;
        while (i < n && isHexDigit(source[i])) i += 1;
        if (i === hexStart) throw new LexError('malformed hex literal', start);
      } else {
        while (i < n && isDigit(source[i])) i += 1;
        if (source[i] === '.') {
          isDouble = true;
          i += 1;
          while (i < n && isDigit(source[i])) i += 1;
        }
        if (source[i] === 'e' || source[i] === 'E') {
          isDouble = true;
          i += 1;
          if (source[i] === '+' || source[i] === '-') i += 1;
          const expStart = i;
          while (i < n && isDigit(source[i])) i += 1;
          if (i === expStart) throw new LexError('malformed exponent', start);
        }
      }
      let literal = source.slice(start, i);
      if (source[i] === 'u' || source[i] === 'U') {
        if (isDouble) throw new LexError('unsigned double literal', start);
        i += 1;
        push('UINT', literal, start);
        continue;
      }
      if (isDouble) {
        push('DOUBLE', literal, start);
      } else {
        push('INT', literal, start);
      }
      continue;
    }

    if (ch === '"' || ch === "'") {
      const quote = ch;
      i += 1;
      let raw = '';
      while (i < n && source[i] !== quote) {
        if (source[i] === '\\') {
          raw += source[i];
          i += 1;
          if (i >= n) throw new LexError('unterminated escape', start);
          raw += source[i];
          i += 1;
        } else {
          raw += source[i];
          i += 1;
        }
      }
      if (i >= n) throw new LexError('unterminated string', start);
      i += 1; // closing quote
      if (source[i] === 'b' || source[i] === 'B') {
        i += 1;
        push('BYTES', raw, start);
      } else {
        push('STRING', raw, start);
      }
      continue;
    }

    const three = source.slice(i, i + 3);
    if (THREE_CHAR_OPS.includes(three)) {
      push('OP', three, start);
      i += 3;
      continue;
    }
    const two = source.slice(i, i + 2);
    // Comments: CEL does not officially support comments, but tolerate // and /* */.
    if (two === '//') {
      i += 2;
      while (i < n && source[i] !== '\n') i += 1;
      continue;
    }
    if (two === '/*') {
      i += 2;
      while (i < n && !(source[i] === '*' && source[i + 1] === '/')) i += 1;
      i += 2;
      continue;
    }
    if (TWO_CHAR_OPS.includes(two)) {
      push('OP', two, start);
      i += 2;
      continue;
    }
    if (SINGLE_CHAR_OPS.has(ch)) {
      push('OP', ch, start);
      i += 1;
      continue;
    }

    throw new LexError(`unexpected character ${JSON.stringify(ch)}`, start);
  }

  push('EOF', '', i);
  return tokens;
}

export function decodeString(raw: string): string {
  let out = '';
  for (let i = 0; i < raw.length; i += 1) {
    const ch = raw[i];
    if (ch !== '\\') {
      out += ch;
      continue;
    }
    i += 1;
    const esc = raw[i];
    switch (esc) {
      case 'n':
        out += '\n';
        break;
      case 't':
        out += '\t';
        break;
      case 'r':
        out += '\r';
        break;
      case 'b':
        out += '\b';
        break;
      case 'f':
        out += '\f';
        break;
      case 'v':
        out += '\v';
        break;
      case 'a':
        out += '\x07';
        break;
      case '\\':
        out += '\\';
        break;
      case '"':
        out += '"';
        break;
      case "'":
        out += "'";
        break;
      case '`':
        out += '`';
        break;
      case '?':
        out += '?';
        break;
      case '/':
        out += '/';
        break;
      case 'x': {
        const hex = raw.slice(i + 1, i + 3);
        out += String.fromCharCode(parseInt(hex, 16));
        i += 2;
        break;
      }
      case 'u': {
        const hex = raw.slice(i + 1, i + 5);
        out += String.fromCharCode(parseInt(hex, 16));
        i += 4;
        break;
      }
      case 'U': {
        const hex = raw.slice(i + 1, i + 9);
        out += String.fromCodePoint(parseInt(hex, 16));
        i += 8;
        break;
      }
      default:
        if (esc >= '0' && esc <= '7') {
          let oct = esc;
          let j = i + 1;
          while (j < raw.length && j < i + 3 && raw[j] >= '0' && raw[j] <= '7') {
            oct += raw[j];
            j += 1;
          }
          out += String.fromCharCode(parseInt(oct, 8));
          i = j - 1;
        } else {
          out += esc;
        }
    }
  }
  return out;
}
