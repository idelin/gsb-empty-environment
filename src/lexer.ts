import { SourceText } from './sourceText';

export const enum LexemeKind {
  Identifier,
  Keyword,
  Number,
  Punct,
}

export interface Lexeme {
  kind: LexemeKind;
  text: string;
  /** UTF-16 offset of the first code unit. */
  start: number;
  /** UTF-16 offset one past the last code unit. */
  end: number;
}

const KEYWORDS = new Set(['let', 'const']);

const IDENT_START = /[$_\p{L}]/u;
const IDENT_PART = /[$\p{L}\p{N}_]/u;

function isIdentStart(cp: number): boolean {
  return IDENT_START.test(String.fromCodePoint(cp));
}

function isIdentPart(cp: number): boolean {
  return IDENT_PART.test(String.fromCodePoint(cp));
}

function isDigit(cp: number): boolean {
  return cp >= 0x30 && cp <= 0x39;
}

function isWhitespace(cp: number): boolean {
  return cp === 0x20 || cp === 0x09 || cp === 0x0a || cp === 0x0d || cp === 0x0b || cp === 0x0c;
}

function width(cp: number): number {
  return cp > 0xffff ? 2 : 1;
}

/**
 * Tokenize the source. All offsets are UTF-16 code-unit offsets.
 * Comments and string literals are skipped (they carry no semantic bindings).
 */
export function lex(source: SourceText): Lexeme[] {
  const out: Lexeme[] = [];
  const n = source.scalarLength;
  let i = 0;

  const push = (kind: LexemeKind, startScalar: number, endScalar: number) => {
    out.push({
      kind,
      text: source.sliceScalars(startScalar, endScalar),
      start: source.scalarIndexToUtf16Offset(startScalar),
      end: source.scalarIndexToUtf16Offset(endScalar),
    });
  };

  while (i < n) {
    const cp = source.scalarAt(i);

    if (isWhitespace(cp)) {
      i++;
      continue;
    }

    // Line comment.
    if (cp === 0x2f && i + 1 < n && source.scalarAt(i + 1) === 0x2f) {
      while (i < n && source.scalarAt(i) !== 0x0a) i++;
      continue;
    }

    // Block comment.
    if (cp === 0x2f && i + 1 < n && source.scalarAt(i + 1) === 0x2a) {
      i += 2;
      while (i + 1 < n && !(source.scalarAt(i) === 0x2a && source.scalarAt(i + 1) === 0x2f)) i++;
      i = Math.min(i + 2, n);
      continue;
    }

    // String literal (skipped, escapes honored).
    if (cp === 0x22 || cp === 0x27) {
      const quote = cp;
      i++;
      while (i < n && source.scalarAt(i) !== quote) {
        i += source.scalarAt(i) === 0x5c ? 2 : 1;
      }
      i = Math.min(i + 1, n);
      continue;
    }

    if (isDigit(cp)) {
      const start = i;
      while (i < n && isDigit(source.scalarAt(i))) i++;
      push(LexemeKind.Number, start, i);
      continue;
    }

    if (isIdentStart(cp)) {
      const start = i;
      i++;
      while (i < n && isIdentPart(source.scalarAt(i))) i++;
      const text = source.sliceScalars(start, i);
      push(KEYWORDS.has(text) ? LexemeKind.Keyword : LexemeKind.Identifier, start, i);
      continue;
    }

    // Anything else is punctuation; offsets derived from scalar index.
    const start = source.scalarIndexToUtf16Offset(i);
    out.push({
      kind: LexemeKind.Punct,
      text: String.fromCodePoint(cp),
      start,
      end: start + width(cp),
    });
    i++;
  }

  return out;
}
