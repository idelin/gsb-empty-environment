import { AbbreviationDictionary } from './dictionary.ts';

/** Half-open source span [start, end) of a committed sentence. */
export interface SentenceSpan {
  start: number;
  end: number;
}

export interface ScanOptions {
  dictionary: AbbreviationDictionary;
  /**
   * When false (streaming), a possible boundary at the end of the buffer is
   * left undecided: the tail stays in the pending region. When true (flush /
   * batch), end-of-buffer confirms any pending boundary.
   */
  finalize: boolean;
  /** Quote stack carried over from the previously committed prefix. */
  initialQuoteStack?: readonly string[];
}

export interface ScanResult {
  sentences: SentenceSpan[];
  /** Number of leading characters finalized by this scan. */
  consumed: number;
  /** Quote stack as of `consumed`, to seed the next scan of the remainder. */
  quoteStack: string[];
}

const TERMINAL_PUNCT = new Set(['.', '!', '?']);
const QUOTE_CHARS = new Set(['"', "'", '(', ')', '[', ']']);
const CLOSE_TO_OPEN: Record<string, string> = { ')': '(', ']': '[' };
const LEADING_TOKEN_NOISE = new Set(['"', "'", '(', '[', '‘', '’', '“', '”']);
const LETTER_OR_DIGIT = /[\p{L}\p{N}]/u;
const WHITESPACE = /\s/;

function isSpace(ch: string | undefined): boolean {
  return ch !== undefined && WHITESPACE.test(ch);
}

function isLetterOrDigit(ch: string | undefined): boolean {
  return ch !== undefined && LETTER_OR_DIGIT.test(ch);
}

/** A quote char sandwiched between letters/digits is an apostrophe ("It's"). */
function isApostrophe(text: string, index: number): boolean {
  const ch = text[index];
  if (ch !== "'" && ch !== '"') return false;
  return isLetterOrDigit(text[index - 1]) && isLetterOrDigit(text[index + 1]);
}

/** Update the quote stack for a quote character encountered in normal flow. */
function applyQuote(text: string, index: number, stack: string[]): void {
  const ch = text[index];
  if (ch === '(' || ch === '[') {
    stack.push(ch);
    return;
  }
  if (ch === ')' || ch === ']') {
    if (stack[stack.length - 1] === CLOSE_TO_OPEN[ch]) stack.pop();
    return;
  }
  if (isApostrophe(text, index)) return;
  if (stack[stack.length - 1] === ch) stack.pop();
  else stack.push(ch);
}

/** True when the quote char at `index` closes the current innermost quote. */
function closesQuote(text: string, index: number, stack: readonly string[]): boolean {
  const ch = text[index];
  if (ch === ')' || ch === ']') return stack[stack.length - 1] === CLOSE_TO_OPEN[ch];
  if (ch === "'" || ch === '"') {
    return !isApostrophe(text, index) && stack[stack.length - 1] === ch;
  }
  return false;
}

/** Token ending at the period (inclusive), used for dictionary lookup. */
function abbreviationToken(text: string, periodIndex: number): string {
  let start = periodIndex - 1;
  while (start >= 0 && !isSpace(text[start])) start--;
  let token = text.slice(start + 1, periodIndex + 1);
  while (token.length > 1 && LEADING_TOKEN_NOISE.has(token[0])) {
    token = token.slice(1);
  }
  return token;
}

/**
 * Core sentence-boundary state machine. Scans `text` and returns the spans
 * that are provably complete. Anything not yet decidable (a period that may
 * be an abbreviation, a terminator with no following context, an unfinished
 * sentence) is left in the unconsumed tail.
 */
export function scan(text: string, options: ScanOptions): ScanResult {
  const { dictionary, finalize } = options;
  const stack: string[] = [...(options.initialQuoteStack ?? [])];
  let stackAtConsumed: string[] = [...stack];
  const sentences: SentenceSpan[] = [];
  const n = text.length;
  let i = 0;
  let sentStart = -1;

  const commit = (end: number, nextStack: readonly string[]): void => {
    sentences.push({ start: sentStart, end });
    sentStart = -1;
    stack.length = 0;
    stack.push(...nextStack);
    stackAtConsumed = [...stack];
  };

  while (i < n) {
    const ch = text[i];

    if (sentStart === -1) {
      if (isSpace(ch)) {
        i++;
        continue;
      }
      sentStart = i;
    }

    if (QUOTE_CHARS.has(ch)) {
      applyQuote(text, i, stack);
      i++;
      continue;
    }

    if (!TERMINAL_PUNCT.has(ch)) {
      i++;
      continue;
    }

    if (ch === '.') {
      const token = abbreviationToken(text, i);
      if (dictionary.has(token)) {
        i++;
        continue;
      }
      if (!finalize && i + 1 === n && dictionary.hasPrefix(token)) {
        // "U." at the buffer end may still grow into "U.S." / "U.S.A.".
        break;
      }
    }

    // Trial scan: absorb a run of terminal marks ("!!", "?!", "..."), then
    // any consecutive closing quotes that pair with quotes opened earlier.
    let k = i;
    while (k < n && TERMINAL_PUNCT.has(text[k])) k++;
    const trialStack = [...stack];
    while (k < n && QUOTE_CHARS.has(text[k]) && closesQuote(text, k, trialStack)) {
      applyQuote(text, k, trialStack);
      k++;
    }

    if (k === n) {
      // End of buffer: boundary is undecided unless we are finalizing.
      if (finalize) commit(k, trialStack);
      break;
    }

    if (isSpace(text[k])) {
      commit(k, trialStack);
      i = k;
      continue;
    }

    // No whitespace after the terminator: not a boundary ("3.14", "a.b").
    i = k;
  }

  if (finalize && sentStart !== -1) {
    // Trailing sentence without a terminator.
    sentences.push({ start: sentStart, end: n });
    sentStart = -1;
    stackAtConsumed = [...stack];
  }

  const consumed = sentences.length === 0 ? 0 : sentences[sentences.length - 1].end;
  return { sentences, consumed, quoteStack: stackAtConsumed };
}
