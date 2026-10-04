export interface QuotePair {
  open: string;
  close: string;
}

export interface Sentence {
  text: string;
  /** Global stream offset, inclusive. Stable once committed. */
  start: number;
  /** Global stream offset, exclusive. Stable once committed. */
  end: number;
}

export interface SegmenterOptions {
  /** version -> abbreviation tokens (without the trailing period, e.g. "Dr", "U.S") */
  dictionaries: Record<string, readonly string[]>;
  /** initial dictionary version; defaults to the first key of `dictionaries` */
  dictionaryVersion?: string;
  /** quote pairs; defaults to "", '', “”, ‘’, 「」, 『』 */
  quotePairs?: readonly QuotePair[];
  /** terminator characters; defaults to ".!?。！？…" */
  terminators?: string;
}

const DEFAULT_QUOTE_PAIRS: readonly QuotePair[] = [
  { open: '"', close: '"' },
  { open: "'", close: "'" },
  { open: "“", close: "”" },
  { open: "‘", close: "’" },
  { open: "「", close: "」" },
  { open: "『", close: "』" },
];

const DEFAULT_TERMINATORS = ".!?。！？…";

function isSpace(ch: string): boolean {
  return ch === " " || ch === "\t" || ch === "\n" || ch === "\r" || ch === "\f" || ch === "\v";
}

function isDigit(ch: string): boolean {
  return ch >= "0" && ch <= "9";
}

function isAsciiLetter(ch: string): boolean {
  return (ch >= "a" && ch <= "z") || (ch >= "A" && ch <= "Z");
}

/**
 * Incremental sentence segmenter.
 *
 * Rules, in order of precedence:
 * 1. A "." is NOT a terminator when
 *    - it is immediately followed by another "." (ellipsis run), or
 *    - it is immediately followed by a letter or digit ("U.S", "2.5", "Mr.Dr"), or
 *    - the maximal run of [A-Za-z.] ending just before it is in the active
 *      abbreviation dictionary (longest token wins, so prefix-overlapping
 *      entries like "Mr" vs "Mr.Dr" resolve to the full token).
 * 2. A terminator ends a sentence; immediately following closing quotes that
 *    match the open-quote stack are absorbed into the sentence.
 * 3. A boundary is committed only once a further character exists after it
 *    (or on flush). While the buffer ends right after a terminator or closing
 *    quote, the tail stays pending — abbreviation periods are never committed
 *    early.
 *
 * Committed sentences carry global source offsets that never change, even if
 * the dictionary version is switched afterwards; the new version only affects
 * the still-pending tail and future input.
 */
export class SentenceSegmenter {
  private buffer = "";
  private bufferStart = 0;
  private readonly dictionaries = new Map<string, ReadonlySet<string>>();
  private activeDict: ReadonlySet<string>;
  private version: string;
  private readonly closeOfOpen = new Map<string, string>();
  private readonly terminatorSet: Set<string>;

  constructor(options: SegmenterOptions) {
    const versions = Object.keys(options.dictionaries);
    if (versions.length === 0) {
      throw new Error("at least one dictionary version is required");
    }
    for (const [name, entries] of Object.entries(options.dictionaries)) {
      this.dictionaries.set(name, new Set(entries));
    }
    const initial = options.dictionaryVersion ?? versions[0];
    const dict = this.dictionaries.get(initial);
    if (dict === undefined) {
      throw new Error(`unknown dictionary version: ${initial}`);
    }
    this.version = initial;
    this.activeDict = dict;
    for (const pair of options.quotePairs ?? DEFAULT_QUOTE_PAIRS) {
      this.closeOfOpen.set(pair.open, pair.close);
    }
    this.terminatorSet = new Set(options.terminators ?? DEFAULT_TERMINATORS);
  }

  get dictionaryVersion(): string {
    return this.version;
  }

  /** Switch the abbreviation dictionary. Affects only pending/future text. */
  setDictionaryVersion(version: string): void {
    const dict = this.dictionaries.get(version);
    if (dict === undefined) {
      throw new Error(`unknown dictionary version: ${version}`);
    }
    this.version = version;
    this.activeDict = dict;
  }

  /** Uncommitted tail of the stream. */
  get pending(): string {
    return this.buffer;
  }

  /** Global stream offset of `pending[0]`. */
  get pendingStart(): number {
    return this.bufferStart;
  }

  /** Feed a chunk (may be empty). Returns newly committed sentences. */
  push(chunk: string): Sentence[] {
    if (chunk.length > 0) {
      this.buffer += chunk;
    }
    return this.drain(false);
  }

  /** End of input: commit everything still pending. */
  flush(): Sentence[] {
    return this.drain(true);
  }

  private drain(endOfInput: boolean): Sentence[] {
    const committed: Sentence[] = [];
    const buf = this.buffer;
    const len = buf.length;
    const stack: string[] = [];
    let cut = 0;

    let i = 0;
    while (i < len && isSpace(buf[i])) i++;
    let start = i;

    while (i < len) {
      const ch = buf[i];
      if (isSpace(ch)) {
        i++;
        continue;
      }
      const top = stack.length > 0 ? stack[stack.length - 1] : undefined;
      if (top !== undefined && ch === this.closeOfOpen.get(top)) {
        stack.pop();
        i++;
        continue;
      }
      if (this.closeOfOpen.has(ch)) {
        stack.push(ch);
        i++;
        continue;
      }
      if (this.terminatorSet.has(ch)) {
        if (ch === "." && this.isNonTerminalDot(buf, i)) {
          i++;
          continue;
        }
        // absorb trailing terminator runs ("?!", "!...") and matching closers
        // in any order ("yes". -> closers then a final dot)
        let j = i + 1;
        for (;;) {
          const before = j;
          while (j < len && this.terminatorSet.has(buf[j])) j++;
          while (j < len && stack.length > 0 && buf[j] === this.closeOfOpen.get(stack[stack.length - 1])) {
            stack.pop();
            j++;
          }
          if (j === before) break;
        }
        if (j < len || endOfInput) {
          committed.push({
            text: buf.slice(start, j),
            start: this.bufferStart + start,
            end: this.bufferStart + j,
          });
          stack.length = 0;
          i = j;
          while (i < len && isSpace(buf[i])) i++;
          start = i;
          cut = i;
          continue;
        }
        // buffer ends right after the terminator/closers: boundary undetermined
        break;
      }
      i++;
    }

    if (endOfInput && start < len) {
      let end = len;
      while (end > start && isSpace(buf[end - 1])) end--;
      if (end > start) {
        committed.push({
          text: buf.slice(start, end),
          start: this.bufferStart + start,
          end: this.bufferStart + end,
        });
      }
      cut = len;
    }

    this.buffer = buf.slice(cut);
    this.bufferStart += cut;
    return committed;
  }

  private isNonTerminalDot(buf: string, i: number): boolean {
    const next = i + 1 < buf.length ? buf[i + 1] : "";
    if (next === "." || isAsciiLetter(next) || isDigit(next)) {
      return true;
    }
    let k = i - 1;
    while (k >= 0 && (isAsciiLetter(buf[k]) || buf[k] === ".")) k--;
    const token = buf.slice(k + 1, i);
    return token.length > 0 && this.activeDict.has(token);
  }
}

/** Segment a complete string in one shot (batch state machine). */
export function segment(text: string, options: SegmenterOptions): Sentence[] {
  const segmenter = new SentenceSegmenter(options);
  const out = segmenter.push(text);
  out.push(...segmenter.flush());
  return out;
}
