import { AbbreviationDictionary } from './dictionary.ts';
import { scan } from './scanner.ts';

/** A committed sentence with stable offsets into the concatenated input. */
export interface Sentence {
  text: string;
  /** Inclusive global offset. */
  start: number;
  /** Exclusive global offset. */
  end: number;
}

export interface SegmenterOptions {
  /** Abbreviation lists keyed by dictionary version. */
  dictionaries: Record<string, readonly string[]>;
  /** Initially active version; defaults to the first key of `dictionaries`. */
  version?: string;
}

/**
 * Chunked (streaming) sentence segmenter.
 *
 * Input arrives via `feed()`. Only sentences whose boundaries are fully
 * determined are committed and returned; the undecided tail (the pending
 * region) is kept internally and re-examined on the next feed. Switching the
 * dictionary version affects only the pending region — sentences that were
 * already committed keep their source spans.
 */
export class SentenceSegmenter {
  private readonly dictionaries = new Map<string, AbbreviationDictionary>();
  private version: string;
  private pending = '';
  private baseOffset = 0;
  private quoteStack: string[] = [];

  constructor(options: SegmenterOptions) {
    const versions = Object.keys(options.dictionaries);
    if (versions.length === 0) {
      throw new Error('at least one dictionary version is required');
    }
    for (const [name, entries] of Object.entries(options.dictionaries)) {
      this.dictionaries.set(name, new AbbreviationDictionary(entries));
    }
    this.version = options.version ?? versions[0];
    if (!this.dictionaries.has(this.version)) {
      throw new Error(`unknown dictionary version: ${this.version}`);
    }
  }

  get dictionaryVersion(): string {
    return this.version;
  }

  /** Switch the active dictionary version. Committed sentences are unaffected. */
  setDictionaryVersion(version: string): void {
    if (!this.dictionaries.has(version)) {
      throw new Error(`unknown dictionary version: ${version}`);
    }
    this.version = version;
  }

  /** Feed a chunk of input; returns sentences committed by this chunk. */
  feed(chunk: string): Sentence[] {
    if (chunk.length === 0) return [];
    this.pending += chunk;
    return this.drain(false);
  }

  /** End of input: commit everything still pending, including a final sentence without a terminator. */
  flush(): Sentence[] {
    const sentences = this.drain(true);
    this.baseOffset += this.pending.length;
    this.pending = '';
    this.quoteStack = [];
    return sentences;
  }

  /** Drop all buffered state and restart offsets at zero. */
  reset(): void {
    this.pending = '';
    this.baseOffset = 0;
    this.quoteStack = [];
  }

  private drain(finalize: boolean): Sentence[] {
    const dictionary = this.dictionaries.get(this.version)!;
    const result = scan(this.pending, {
      dictionary,
      finalize,
      initialQuoteStack: this.quoteStack,
    });
    const sentences = result.sentences.map((span) => ({
      text: this.pending.slice(span.start, span.end),
      start: this.baseOffset + span.start,
      end: this.baseOffset + span.end,
    }));
    this.pending = this.pending.slice(result.consumed);
    this.baseOffset += result.consumed;
    this.quoteStack = result.quoteStack;
    return sentences;
  }
}

/**
 * Batch reference: segment the whole text in one pass with the full state
 * machine (equivalent to feeding everything at once and flushing).
 */
export function segmentAll(text: string, options: SegmenterOptions): Sentence[] {
  const versions = Object.keys(options.dictionaries);
  if (versions.length === 0) {
    throw new Error('at least one dictionary version is required');
  }
  const version = options.version ?? versions[0];
  const entries = options.dictionaries[version];
  if (entries === undefined) {
    throw new Error(`unknown dictionary version: ${version}`);
  }
  const dictionary = new AbbreviationDictionary(entries);
  const result = scan(text, { dictionary, finalize: true });
  return result.sentences.map((span) => ({
    text: text.slice(span.start, span.end),
    start: span.start,
    end: span.end,
  }));
}
