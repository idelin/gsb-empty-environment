/**
 * Incremental UTF-8 decoder over arbitrarily chunked input.
 *
 * - Decoding follows the WHATWG UTF-8 state machine, so replacement-mode
 *   output (one U+FFFD per maximal ill-formed subpart) matches the platform
 *   TextDecoder regardless of how input is chunked.
 * - Errors in strict mode carry the absolute byte offset of the first byte
 *   of the ill-formed sequence; a truncated sequence is only reported by
 *   finish(), never by push().
 * - Every decoded unit updates a compressed OffsetMap (byte <-> UTF-16).
 * - checkpoint() returns a JSON-serializable snapshot bound to a SHA-256
 *   digest of the consumed prefix; resume() refuses a different stream.
 */

import { createHash, type Hash } from 'node:crypto';
import { OffsetMap, type OffsetMapJSON } from './offset_map.ts';

export type ErrorStrategy = 'strict' | 'replace';

export interface DecoderOptions {
  /** 'strict' throws Utf8DecodeError; 'replace' emits U+FFFD. Default 'replace'. */
  strategy?: ErrorStrategy;
}

export type DecodeErrorKind = 'invalid' | 'truncated';

export class Utf8DecodeError extends Error {
  /** Absolute offset of the first byte of the ill-formed sequence. */
  readonly byteOffset: number;
  /** Absolute offset of the byte at which the error was detected. */
  readonly detectedAt: number;
  readonly kind: DecodeErrorKind;

  constructor(kind: DecodeErrorKind, byteOffset: number, detectedAt: number) {
    super(
      kind === 'truncated'
        ? `Truncated UTF-8 sequence at absolute byte offset ${byteOffset}`
        : `Invalid UTF-8 sequence at absolute byte offset ${byteOffset}`,
    );
    this.name = 'Utf8DecodeError';
    this.kind = kind;
    this.byteOffset = byteOffset;
    this.detectedAt = detectedAt;
  }
}

export interface DecodeResult {
  text: string;
  byteLength: number;
  utf16Length: number;
}

export interface Checkpoint {
  version: 1;
  strategy: ErrorStrategy;
  needed: number;
  codePoint: number;
  lower: number;
  upper: number;
  seqStart: number;
  seqLen: number;
  consumed: number;
  utf16Pos: number;
  text: string;
  map: OffsetMapJSON;
  /** SHA-256 (hex) of every byte consumed so far; binds the checkpoint to its stream. */
  digest: string;
}

const REPLACEMENT = '�';

export class IncrementalUtf8Decoder {
  private readonly strategy: ErrorStrategy;
  private needed = 0;
  private codePoint = 0;
  private lower = 0x80;
  private upper = 0xbf;
  private seqStart = 0;
  private seqLen = 0;
  private consumed = 0;
  private utf16Pos = 0;
  private parts: string[] = [];
  private hash: Hash = createHash('sha256');
  private finished = false;

  readonly map = new OffsetMap();

  constructor(options: DecoderOptions = {}) {
    this.strategy = options.strategy ?? 'replace';
  }

  get byteLength(): number {
    return this.consumed;
  }

  get utf16Length(): number {
    return this.utf16Pos;
  }

  get text(): string {
    return this.parts.join('');
  }

  get isFinished(): boolean {
    return this.finished;
  }

  /** Feed the next chunk. Chunk boundaries never change the decode result. */
  push(chunk: Uint8Array): void {
    if (this.finished) throw new Error('decoder already finished');
    this.hash.update(chunk);
    let i = 0;
    while (i < chunk.length) {
      const byte = chunk[i];
      const absolute = this.consumed + i;
      if (this.needed === 0) {
        if (byte <= 0x7f) {
          this.emitScalar(byte, 1);
          i++;
        } else if (byte >= 0xc2 && byte <= 0xdf) {
          this.beginSequence(byte & 0x1f, 1, absolute);
          i++;
        } else if (byte >= 0xe0 && byte <= 0xef) {
          this.lower = byte === 0xe0 ? 0xa0 : 0x80;
          this.upper = byte === 0xed ? 0x9f : 0xbf;
          this.beginSequence(byte & 0x0f, 2, absolute);
          i++;
        } else if (byte >= 0xf0 && byte <= 0xf4) {
          this.lower = byte === 0xf0 ? 0x90 : 0x80;
          this.upper = byte === 0xf4 ? 0x8f : 0xbf;
          this.beginSequence(byte & 0x07, 3, absolute);
          i++;
        } else {
          // Byte cannot start a sequence (0x80..0xC1, 0xF5..0xFF).
          if (this.strategy === 'strict') {
            throw new Utf8DecodeError('invalid', absolute, absolute);
          }
          this.emitReplacement(1);
          i++;
        }
      } else if (byte < this.lower || byte > this.upper) {
        // Continuation byte out of range: replace the consumed subpart,
        // then reprocess this byte from a fresh state (WHATWG behavior).
        if (this.strategy === 'strict') {
          throw new Utf8DecodeError('invalid', this.seqStart, absolute);
        }
        this.emitReplacement(this.seqLen);
        this.resetSequence();
      } else {
        this.lower = 0x80;
        this.upper = 0xbf;
        this.codePoint = (this.codePoint << 6) | (byte & 0x3f);
        this.needed--;
        this.seqLen++;
        i++;
        if (this.needed === 0) {
          this.emitScalar(this.codePoint, this.seqLen);
          this.resetSequence();
        }
      }
    }
    this.consumed += chunk.length;
  }

  /**
   * Flush the decoder. Only here is a truncated tail sequence resolved:
   * strict mode throws, replace mode emits a single U+FFFD covering it.
   */
  finish(): DecodeResult {
    if (this.finished) throw new Error('decoder already finished');
    this.finished = true;
    if (this.needed > 0) {
      if (this.strategy === 'strict') {
        throw new Utf8DecodeError('truncated', this.seqStart, this.consumed);
      }
      this.emitReplacement(this.seqLen);
      this.resetSequence();
    }
    return {
      text: this.text,
      byteLength: this.consumed,
      utf16Length: this.utf16Pos,
    };
  }

  /** JSON-serializable snapshot, bound to the consumed prefix via SHA-256. */
  checkpoint(): Checkpoint {
    return {
      version: 1,
      strategy: this.strategy,
      needed: this.needed,
      codePoint: this.codePoint,
      lower: this.lower,
      upper: this.upper,
      seqStart: this.seqStart,
      seqLen: this.seqLen,
      consumed: this.consumed,
      utf16Pos: this.utf16Pos,
      text: this.text,
      map: this.map.toJSON(),
      digest: this.hash.copy().digest('hex'),
    };
  }

  /**
   * Restore from a checkpoint. `prefix` must be exactly the bytes consumed
   * when the checkpoint was taken; a digest mismatch means the caller is
   * trying to continue a different stream and is rejected.
   */
  static resume(checkpoint: Checkpoint, prefix: Uint8Array): IncrementalUtf8Decoder {
    if (checkpoint.version !== 1) {
      throw new Error(`unsupported checkpoint version ${checkpoint.version}`);
    }
    const digest = createHash('sha256').update(prefix).digest('hex');
    if (digest !== checkpoint.digest || prefix.length !== checkpoint.consumed) {
      throw new Error(
        'checkpoint digest mismatch: refusing to resume a different input stream',
      );
    }
    const decoder = new IncrementalUtf8Decoder({ strategy: checkpoint.strategy });
    decoder.needed = checkpoint.needed;
    decoder.codePoint = checkpoint.codePoint;
    decoder.lower = checkpoint.lower;
    decoder.upper = checkpoint.upper;
    decoder.seqStart = checkpoint.seqStart;
    decoder.seqLen = checkpoint.seqLen;
    decoder.consumed = checkpoint.consumed;
    decoder.utf16Pos = checkpoint.utf16Pos;
    decoder.parts = [checkpoint.text];
    decoder.hash = createHash('sha256').update(prefix);
    const restored = OffsetMap.fromJSON(checkpoint.map);
    (decoder as { map: OffsetMap }).map = restored;
    return decoder;
  }

  private beginSequence(codePoint: number, needed: number, absolute: number): void {
    this.codePoint = codePoint;
    this.needed = needed;
    this.seqStart = absolute;
    this.seqLen = 1;
  }

  private resetSequence(): void {
    this.needed = 0;
    this.codePoint = 0;
    this.lower = 0x80;
    this.upper = 0xbf;
    this.seqLen = 0;
  }

  private emitScalar(codePoint: number, byteLen: number): void {
    const utf16Len = codePoint > 0xffff ? 2 : 1;
    this.parts.push(String.fromCodePoint(codePoint));
    this.map.addUnit(byteLen, utf16Len, false);
    this.utf16Pos += utf16Len;
  }

  private emitReplacement(byteLen: number): void {
    this.parts.push(REPLACEMENT);
    this.map.addUnit(byteLen, 1, true);
    this.utf16Pos += 1;
  }
}
