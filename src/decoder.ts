import { createHash, type Hash } from 'node:crypto';
import { OffsetMap, type RunTuple } from './offset-map.ts';

export type Strategy = 'strict' | 'replace';

export type DecodeErrorKind = 'invalid-lead' | 'invalid-continuation' | 'truncated';

export class Utf8DecodeError extends Error {
  readonly kind: DecodeErrorKind;
  /** Absolute byte offset of the first byte of the offending sequence. */
  readonly byteOffset: number;
  /** The consumed bytes of the offending sequence. */
  readonly sequence: Uint8Array;

  constructor(kind: DecodeErrorKind, byteOffset: number, sequence: Uint8Array) {
    super(`Invalid UTF-8 (${kind}) at absolute byte offset ${byteOffset}`);
    this.name = 'Utf8DecodeError';
    this.kind = kind;
    this.byteOffset = byteOffset;
    this.sequence = sequence;
  }
}

export class CheckpointMismatchError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CheckpointMismatchError';
  }
}

export interface DecodeEvent {
  kind: 'text' | 'replacement';
  /** Absolute byte range covered by this event: [byteStart, byteEnd). */
  byteStart: number;
  byteEnd: number;
  /** UTF-16 code-unit offset of the emitted text. */
  u16Start: number;
  /** Length in UTF-16 code units (1 or 2). */
  u16Length: number;
  text: string;
}

/** JSON-serializable decoder checkpoint. */
export interface Checkpoint {
  version: 1;
  strategy: Strategy;
  byteOffset: number;
  u16Offset: number;
  pending: number[];
  needed: number;
  lower: number;
  upper: number;
  codePoint: number;
  seqStart: number;
  runs: RunTuple[];
  /** SHA-256 hex digest of every byte consumed so far; binds the checkpoint to its input stream. */
  prefixDigest: string;
}

const REPLACEMENT = '�';

/**
 * Incremental UTF-8 decoder. Bytes may be pushed in arbitrarily sized chunks;
 * chunk boundaries never change the emitted events, the offset map, or the
 * position/kind of errors.
 *
 * Strategies:
 *  - 'strict':  the first invalid sequence throws Utf8DecodeError carrying the
 *               absolute byte offset. A sequence left incomplete at finish()
 *               is reported as 'truncated'.
 *  - 'replace': each invalid sequence emits exactly one U+FFFD. Consumption
 *               follows the WHATWG UTF-8 decoder: the maximal valid prefix of
 *               the sequence is consumed, the offending byte is re-examined.
 *               A multi-byte invalid sequence therefore maps k bytes to one
 *               code unit (a many-to-one segment in the offset map).
 */
export class IncrementalUtf8Decoder {
  readonly strategy: Strategy;
  private _byteOffset = 0;
  private _u16Offset = 0;
  private pending: number[] = [];
  private needed = 0;
  private lower = 0x80;
  private upper = 0xbf;
  private codePoint = 0;
  private seqStart = 0;
  private finished = false;
  private hash: Hash = createHash('sha256');
  private readonly offsetMap = new OffsetMap();

  constructor(strategy: Strategy = 'strict') {
    this.strategy = strategy;
  }

  /** Absolute offset of the next byte to be consumed. */
  get byteOffset(): number {
    return this._byteOffset;
  }

  /** UTF-16 code units emitted so far. */
  get u16Offset(): number {
    return this._u16Offset;
  }

  /** The incrementally built, run-compressed byte<->UTF-16 offset map. */
  get map(): OffsetMap {
    return this.offsetMap;
  }

  /** SHA-256 hex digest of all bytes consumed so far. */
  prefixDigest(): string {
    return this.hash.copy().digest('hex');
  }

  push(chunk: Uint8Array): DecodeEvent[] {
    if (this.finished) throw new Error('decoder already finished');
    this.hash.update(chunk);
    const events: DecodeEvent[] = [];
    let i = 0;
    while (i < chunk.length) {
      if (this.step(chunk[i], events)) i += 1;
    }
    return events;
  }

  /**
   * Signal end of input. Only here is an incomplete pending sequence
   * resolved: 'truncated' error in strict mode, one U+FFFD in replace mode.
   */
  finish(): DecodeEvent[] {
    if (this.finished) throw new Error('decoder already finished');
    this.finished = true;
    const events: DecodeEvent[] = [];
    if (this.needed > 0) {
      this.error('truncated', events);
    }
    return events;
  }

  /** Serialize the full decoder state (including the offset map) to JSON-safe data. */
  checkpoint(): Checkpoint {
    if (this.finished) throw new Error('decoder already finished');
    return {
      version: 1,
      strategy: this.strategy,
      byteOffset: this._byteOffset,
      u16Offset: this._u16Offset,
      pending: [...this.pending],
      needed: this.needed,
      lower: this.lower,
      upper: this.upper,
      codePoint: this.codePoint,
      seqStart: this.seqStart,
      runs: this.offsetMap.toJSON(),
      prefixDigest: this.prefixDigest(),
    };
  }

  /**
   * Resume decoding from a checkpoint. `prefix` must be exactly the bytes
   * consumed before the checkpoint; its SHA-256 digest is verified against
   * the checkpoint, so a checkpoint cannot be continued on another stream.
   */
  static resume(checkpoint: Checkpoint, prefix: Uint8Array): IncrementalUtf8Decoder {
    if (checkpoint.version !== 1) {
      throw new CheckpointMismatchError(`unsupported checkpoint version ${String(checkpoint.version)}`);
    }
    if (prefix.length !== checkpoint.byteOffset) {
      throw new CheckpointMismatchError(
        `prefix length ${prefix.length} does not match checkpoint byte offset ${checkpoint.byteOffset}`,
      );
    }
    const digest = createHash('sha256').update(prefix).digest('hex');
    if (digest !== checkpoint.prefixDigest) {
      throw new CheckpointMismatchError('prefix digest mismatch: refusing to continue a different stream');
    }
    const d = new IncrementalUtf8Decoder(checkpoint.strategy);
    d._byteOffset = checkpoint.byteOffset;
    d._u16Offset = checkpoint.u16Offset;
    d.pending = [...checkpoint.pending];
    d.needed = checkpoint.needed;
    d.lower = checkpoint.lower;
    d.upper = checkpoint.upper;
    d.codePoint = checkpoint.codePoint;
    d.seqStart = checkpoint.seqStart;
    d.hash = createHash('sha256').update(prefix);
    d.offsetMap.restore(checkpoint.runs);
    return d;
  }

  /** Returns true when the byte was consumed and the caller may advance. */
  private step(b: number, events: DecodeEvent[]): boolean {
    if (this.needed === 0) {
      if (b <= 0x7f) {
        this.emitText(b, this._byteOffset, 1, events);
        this._byteOffset += 1;
        return true;
      }
      if (b >= 0xc2 && b <= 0xdf) {
        this.startSeq(b, 2, b & 0x1f, 0x80, 0xbf);
        this._byteOffset += 1;
        return true;
      }
      if (b >= 0xe0 && b <= 0xef) {
        const lo = b === 0xe0 ? 0xa0 : 0x80;
        const hi = b === 0xed ? 0x9f : 0xbf;
        this.startSeq(b, 3, b & 0x0f, lo, hi);
        this._byteOffset += 1;
        return true;
      }
      if (b >= 0xf0 && b <= 0xf4) {
        const lo = b === 0xf0 ? 0x90 : 0x80;
        const hi = b === 0xf4 ? 0x8f : 0xbf;
        this.startSeq(b, 4, b & 0x07, lo, hi);
        this._byteOffset += 1;
        return true;
      }
      // Invalid lead byte: 0x80-0xc1 (stray continuation / overlong) or 0xf5-0xff.
      this.seqStart = this._byteOffset;
      this.pending = [b];
      this._byteOffset += 1;
      this.error('invalid-lead', events);
      return true;
    }
    if (b >= this.lower && b <= this.upper) {
      this.pending.push(b);
      this.codePoint = (this.codePoint << 6) | (b & 0x3f);
      this._byteOffset += 1;
      // The restricted range only applies to the first continuation byte.
      this.lower = 0x80;
      this.upper = 0xbf;
      if (this.pending.length === this.needed) {
        this.emitText(this.codePoint, this.seqStart, this.needed, events);
        this.resetSeq();
      }
      return true;
    }
    // Invalid continuation: consume only the pending prefix, re-examine this byte.
    this.error('invalid-continuation', events);
    return false;
  }

  private startSeq(lead: number, needed: number, codePoint: number, lower: number, upper: number): void {
    this.needed = needed;
    this.codePoint = codePoint;
    this.lower = lower;
    this.upper = upper;
    this.seqStart = this._byteOffset;
    this.pending = [lead];
  }

  private resetSeq(): void {
    this.needed = 0;
    this.pending = [];
    this.codePoint = 0;
    this.lower = 0x80;
    this.upper = 0xbf;
  }

  private emitText(codePoint: number, byteStart: number, byteLen: number, events: DecodeEvent[]): void {
    const text = String.fromCodePoint(codePoint);
    const u16Length = text.length;
    this.offsetMap.append(byteLen, u16Length, false);
    events.push({
      kind: 'text',
      byteStart,
      byteEnd: byteStart + byteLen,
      u16Start: this._u16Offset,
      u16Length,
      text,
    });
    this._u16Offset += u16Length;
  }

  private emitReplacement(byteStart: number, byteLen: number, events: DecodeEvent[]): void {
    this.offsetMap.append(byteLen, 1, true);
    events.push({
      kind: 'replacement',
      byteStart,
      byteEnd: byteStart + byteLen,
      u16Start: this._u16Offset,
      u16Length: 1,
      text: REPLACEMENT,
    });
    this._u16Offset += 1;
  }

  private error(kind: DecodeErrorKind, events: DecodeEvent[]): void {
    const sequence = Uint8Array.from(this.pending);
    const seqStart = this.seqStart;
    const seqLen = this.pending.length;
    this.resetSeq();
    if (this.strategy === 'strict') {
      throw new Utf8DecodeError(kind, seqStart, sequence);
    }
    this.emitReplacement(seqStart, seqLen, events);
  }
}
