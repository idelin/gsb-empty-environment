import { Context, LexState } from './state';

export type TokenType =
  | 'keyword'
  | 'identifier'
  | 'number'
  | 'string'
  | 'comment'
  | 'operator'
  | 'punctuation';

/** Ranges are line-relative UTF-16 code-unit offsets, [start, end). */
export interface Token {
  start: number;
  end: number;
  type: TokenType;
}

export interface LineResult {
  tokens: Token[];
  endState: LexState;
}

const KEYWORDS = new Set([
  'const', 'let', 'var', 'function', 'return', 'if', 'else', 'for', 'while',
  'do', 'switch', 'case', 'default', 'break', 'continue', 'new', 'delete',
  'typeof', 'instanceof', 'in', 'of', 'class', 'extends', 'super', 'this',
  'import', 'export', 'from', 'as', 'try', 'catch', 'finally', 'throw',
  'async', 'await', 'yield', 'static', 'get', 'set', 'true', 'false',
  'null', 'undefined', 'void',
]);

function isIdStart(code: number): boolean {
  return (
    (code >= 65 && code <= 90) || // A-Z
    (code >= 97 && code <= 122) || // a-z
    code === 95 || // _
    code === 36 || // $
    code >= 128 // non-ASCII (incl. surrogate halves): treated as identifier chars
  );
}

function isIdPart(code: number): boolean {
  return isIdStart(code) || (code >= 48 && code <= 57);
}

function isDigit(code: number): boolean {
  return code >= 48 && code <= 57;
}

interface StringScan {
  end: number;
  closed: boolean;
  continued: boolean;
}

/** Scan a '...' or "..." string body starting just after the open quote (or at a line start when continued). */
function scanStringBody(line: string, from: number, quote: string): StringScan {
  const n = line.length;
  let i = from;
  while (i < n) {
    const ch = line[i];
    if (ch === '\\') {
      if (i + 1 >= n) return { end: n, closed: false, continued: true };
      i += 2;
      continue;
    }
    if (ch === quote) return { end: i + 1, closed: true, continued: false };
    i++;
  }
  return { end: n, closed: false, continued: false };
}

interface TemplateScan {
  end: number;
  kind: 'closed' | 'expr' | 'eol';
}

/** Scan template-literal text; stops after '`', before '${', or at end of line. */
function scanTemplateText(line: string, from: number): TemplateScan {
  const n = line.length;
  let i = from;
  while (i < n) {
    const ch = line[i];
    if (ch === '\\') {
      i += i + 1 >= n ? 1 : 2;
      continue;
    }
    if (ch === '`') return { end: i + 1, kind: 'closed' };
    if (ch === '$' && line[i + 1] === '{') return { end: i, kind: 'expr' };
    i++;
  }
  return { end: n, kind: 'eol' };
}

/**
 * Tokenize one line given its input lexical state.
 * The returned endState is the input state for the next line.
 */
export function tokenizeLine(line: string, startState: LexState): LineResult {
  const tokens: Token[] = [];
  const contexts: Context[] = startState.contexts.map((c) => ({ ...c }));
  const n = line.length;
  let i = 0;

  const push = (start: number, end: number, type: TokenType): void => {
    if (end > start) tokens.push({ start, end, type });
  };

  while (i < n) {
    const top = contexts.length > 0 ? contexts[contexts.length - 1] : undefined;

    if (top && top.type === 'blockComment') {
      const close = line.indexOf('*/', i);
      if (close === -1) {
        push(i, n, 'comment');
        i = n;
      } else {
        push(i, close + 2, 'comment');
        contexts.pop();
        i = close + 2;
      }
      continue;
    }

    if (top && top.type === 'continuedString') {
      const scan = scanStringBody(line, i, top.quote);
      push(i, scan.end, 'string');
      i = scan.end;
      if (!scan.continued) contexts.pop();
      continue;
    }

    if (top && top.type === 'template') {
      const scan = scanTemplateText(line, i);
      if (scan.kind === 'expr') {
        push(i, scan.end, 'string');
        push(scan.end, scan.end + 2, 'punctuation');
        contexts.push({ type: 'templateExpr', braceDepth: 0 });
        i = scan.end + 2;
      } else {
        push(i, scan.end, 'string');
        if (scan.kind === 'closed') contexts.pop();
        i = scan.end;
      }
      continue;
    }

    // Normal code mode (also applies inside ${ ... } interpolations).
    const ch = line[i];
    const code = line.charCodeAt(i);

    if (ch === ' ' || ch === '\t' || ch === '\r') {
      i++;
      continue;
    }

    if (ch === '/' && line[i + 1] === '/') {
      push(i, n, 'comment');
      i = n;
      continue;
    }

    if (ch === '/' && line[i + 1] === '*') {
      const close = line.indexOf('*/', i + 2);
      if (close === -1) {
        push(i, n, 'comment');
        contexts.push({ type: 'blockComment' });
        i = n;
      } else {
        push(i, close + 2, 'comment');
        i = close + 2;
      }
      continue;
    }

    if (ch === '"' || ch === "'") {
      const scan = scanStringBody(line, i + 1, ch);
      push(i, scan.end, 'string');
      i = scan.end;
      if (scan.continued) contexts.push({ type: 'continuedString', quote: ch });
      continue;
    }

    if (ch === '`') {
      const scan = scanTemplateText(line, i + 1);
      if (scan.kind === 'expr') {
        push(i, scan.end, 'string');
        push(scan.end, scan.end + 2, 'punctuation');
        contexts.push({ type: 'template' }, { type: 'templateExpr', braceDepth: 0 });
        i = scan.end + 2;
      } else {
        push(i, scan.end, 'string');
        if (scan.kind !== 'closed') contexts.push({ type: 'template' });
        i = scan.end;
      }
      continue;
    }

    if (ch === '{') {
      const t = contexts.length > 0 ? contexts[contexts.length - 1] : undefined;
      if (t && t.type === 'templateExpr') t.braceDepth++;
      push(i, i + 1, 'punctuation');
      i++;
      continue;
    }

    if (ch === '}') {
      const t = contexts.length > 0 ? contexts[contexts.length - 1] : undefined;
      if (t && t.type === 'templateExpr') {
        if (t.braceDepth === 0) {
          contexts.pop(); // end of ${ ... }: resume template text
          push(i, i + 1, 'punctuation');
          i++;
          continue;
        }
        t.braceDepth--;
      }
      push(i, i + 1, 'punctuation');
      i++;
      continue;
    }

    if (isDigit(code)) {
      const start = i;
      if (ch === '0' && (line[i + 1] === 'x' || line[i + 1] === 'X')) {
        i += 2;
        while (i < n && /[0-9a-fA-F]/.test(line[i])) i++;
      } else {
        while (i < n && (isDigit(line.charCodeAt(i)) || line[i] === '.')) i++;
      }
      push(start, i, 'number');
      continue;
    }

    if (isIdStart(code)) {
      const start = i;
      i++;
      while (i < n && isIdPart(line.charCodeAt(i))) i++;
      const word = line.slice(start, i);
      push(start, i, KEYWORDS.has(word) ? 'keyword' : 'identifier');
      continue;
    }

    if ('+-*/%=<>!&|?:^~'.indexOf(ch) !== -1) {
      const start = i;
      while (i < n && '+-*/%=<>!&|?:^~'.indexOf(line[i]) !== -1) i++;
      push(start, i, 'operator');
      continue;
    }

    push(i, i + 1, 'punctuation');
    i++;
  }

  return { tokens, endState: { contexts } };
}

export function tokensEqual(a: Token[], b: Token[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i].start !== b[i].start || a[i].end !== b[i].end || a[i].type !== b[i].type) {
      return false;
    }
  }
  return true;
}
