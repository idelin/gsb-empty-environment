/**
 * Streaming escape codec.
 *
 * Protocol grammar (input is a byte stream, may arrive in arbitrary chunks):
 *
 *   data        := (literal | escape | rawBlock)*
 *   escape      := "\\" ("\\" | "n" | "r" | "t" | "0" | "x" HEX HEX | "R" len ":")
 *   rawBlock    := "\R" len ":" <len raw bytes>   -- backslash is NOT special inside
 *   len         := decimal digits, 0 <= len <= maxRawLength
 *
 * Invalid escapes are emitted literally and decoding resumes at the offending
 * byte, so a bad sequence never swallows following valid characters.
 * Incomplete sequences at end-of-stream are flushed literally by `end()`.
 */

export const DEFAULT_MAX_RAW_LENGTH = 65535;

/** A decoded byte together with the half-open input range it came from. */
export interface DecodedByte {
  byte: number;
  /** Absolute offset (across all pushed chunks) of the first input byte. */
  start: number;
  /** Absolute offset one past the last input byte of this unit. */
  end: number;
}

export interface DecoderOptions {
  /** Upper bound for raw-block lengths. Defaults to {@link DEFAULT_MAX_RAW_LENGTH}. */
  maxRawLength?: number;
}

const BACKSLASH = 0x5c; // \
const COLON = 0x3a; // :

const enum State {
  Data,
  Escape, // saw "\"
  Hex1, // saw "\x"
  Hex2, // saw "\x" + one hex digit
  RawLen, // saw "\R", accumulating decimal digits
  RawBody, // header complete, accumulating raw bytes
}

interface PendingByte {
  byte: number;
  offset: number;
}

function hexDigitValue(byte: number): number {
  if (byte >= 0x30 && byte <= 0x39) return byte - 0x30; // 0-9
  if (byte >= 0x41 && byte <= 0x46) return byte - 0x41 + 10; // A-F
  if (byte >= 0x61 && byte <= 0x66) return byte - 0x61 + 10; // a-f
  return -1;
}

function isDecimalDigit(byte: number): boolean {
  return byte >= 0x30 && byte <= 0x39;
}

export class EscapeDecoder {
  readonly maxRawLength: number;

  private state: State = State.Data;
  /** Bytes of the currently incomplete escape/header, with input offsets. */
  private pending: PendingByte[] = [];
  /** Buffered raw-block body bytes (emitted only when the block completes). */
  private body: PendingByte[] = [];
  private hexValue = 0;
  private rawLength = 0;
  private rawDigits = 0;
  private rawRemaining = 0;
  private offset = 0;
  private ended = false;

  constructor(options: DecoderOptions = {}) {
    const max = options.maxRawLength ?? DEFAULT_MAX_RAW_LENGTH;
    if (!Number.isInteger(max) || max < 0) {
      throw new RangeError(`maxRawLength must be a non-negative integer, got ${max}`);
    }
    this.maxRawLength = max;
  }

  /** Feed a chunk of input; returns the bytes decoded from this chunk. */
  push(chunk: Uint8Array): DecodedByte[] {
    if (this.ended) throw new Error("EscapeDecoder: push() after end()");
    const out: DecodedByte[] = [];
    for (let i = 0; i < chunk.length; i++) {
      this.step(chunk[i], this.offset++, out);
    }
    return out;
  }

  /**
   * Signal end of input. Any incomplete escape sequence or truncated raw
   * block is flushed literally (output bytes equal the buffered input bytes).
   */
  end(): DecodedByte[] {
    if (this.ended) return [];
    this.ended = true;
    const out: DecodedByte[] = [];
    this.flushLiteral(this.pending, out);
    this.flushLiteral(this.body, out);
    this.pending = [];
    this.body = [];
    this.state = State.Data;
    return out;
  }

  private step(byte: number, offset: number, out: DecodedByte[]): void {
    switch (this.state) {
      case State.Data:
        if (byte === BACKSLASH) {
          this.pending.push({ byte, offset });
          this.hexValue = 0;
          this.rawLength = 0;
          this.rawDigits = 0;
          this.state = State.Escape;
        } else {
          out.push({ byte, start: offset, end: offset + 1 });
        }
        return;

      case State.Escape: {
        let value: number;
        switch (byte) {
          case 0x5c: // "\\"
            value = 0x5c;
            break;
          case 0x6e: // "\n"
            value = 0x0a;
            break;
          case 0x72: // "\r"
            value = 0x0d;
            break;
          case 0x74: // "\t"
            value = 0x09;
            break;
          case 0x30: // "\0"
            value = 0x00;
            break;
          case 0x78: // "x" -> hex escape
            this.pending.push({ byte, offset });
            this.state = State.Hex1;
            return;
          case 0x52: // "R" -> raw block
            this.pending.push({ byte, offset });
            this.state = State.RawLen;
            return;
          default:
            this.invalidEscape(byte, offset, out);
            return;
        }
        out.push({ byte: value, start: this.pending[0].offset, end: offset + 1 });
        this.pending = [];
        this.state = State.Data;
        return;
      }

      case State.Hex1:
      case State.Hex2: {
        const digit = hexDigitValue(byte);
        if (digit < 0) {
          this.invalidEscape(byte, offset, out);
          return;
        }
        this.pending.push({ byte, offset });
        if (this.state === State.Hex1) {
          this.hexValue = digit;
          this.state = State.Hex2;
        } else {
          out.push({
            byte: this.hexValue * 16 + digit,
            start: this.pending[0].offset,
            end: offset + 1,
          });
          this.pending = [];
          this.state = State.Data;
        }
        return;
      }

      case State.RawLen:
        if (isDecimalDigit(byte)) {
          this.rawLength = this.rawLength * 10 + (byte - 0x30);
          this.rawDigits++;
          if (this.rawLength > this.maxRawLength) {
            // Length exceeds the configured limit: not a raw block.
            this.invalidEscape(byte, offset, out);
            return;
          }
          this.pending.push({ byte, offset });
          return;
        }
        if (byte === COLON && this.rawDigits > 0) {
          this.pending.push({ byte, offset });
          if (this.rawLength === 0) {
            // Empty raw block decodes to nothing.
            this.pending = [];
            this.state = State.Data;
          } else {
            this.rawRemaining = this.rawLength;
            this.state = State.RawBody;
          }
          return;
        }
        this.invalidEscape(byte, offset, out);
        return;

      case State.RawBody:
        // Raw bytes: backslash and every other byte are literal here.
        this.body.push({ byte, offset });
        if (--this.rawRemaining === 0) {
          for (const b of this.body) {
            out.push({ byte: b.byte, start: b.offset, end: b.offset + 1 });
          }
          this.body = [];
          this.pending = [];
          this.state = State.Data;
        }
        return;
    }
  }

  /**
   * Emit the buffered invalid sequence literally and reprocess the current
   * byte in the data state, so following valid characters are not swallowed.
   */
  private invalidEscape(byte: number, offset: number, out: DecodedByte[]): void {
    this.flushLiteral(this.pending, out);
    this.pending = [];
    this.hexValue = 0;
    this.rawLength = 0;
    this.rawDigits = 0;
    this.state = State.Data;
    this.step(byte, offset, out);
  }

  private flushLiteral(pending: PendingByte[], out: DecodedByte[]): void {
    for (const p of pending) {
      out.push({ byte: p.byte, start: p.offset, end: p.offset + 1 });
    }
  }
}

/** One-shot decode: returns decoded bytes with their input ranges. */
export function decode(input: Uint8Array, options?: DecoderOptions): DecodedByte[] {
  const decoder = new EscapeDecoder(options);
  const out = decoder.push(input);
  return out.concat(decoder.end());
}

/** One-shot decode to a plain byte array (ranges discarded). */
export function decodeToBytes(input: Uint8Array, options?: DecoderOptions): Uint8Array {
  const decoded = decode(input, options);
  const bytes = new Uint8Array(decoded.length);
  for (let i = 0; i < decoded.length; i++) bytes[i] = decoded[i].byte;
  return bytes;
}

/**
 * Streaming decoder that validates UTF-8 on the *decoded* byte stream.
 * Multi-byte sequences may be split across chunk boundaries or produced by
 * hex escapes; validation happens only after escape decoding.
 */
export class Utf8StringDecoder {
  private readonly decoder: EscapeDecoder;
  private readonly utf8 = new TextDecoder("utf-8", { fatal: true });

  constructor(options?: DecoderOptions) {
    this.decoder = new EscapeDecoder(options);
  }

  /** Feed encoded input; returns decoded text. Throws on invalid UTF-8. */
  push(chunk: Uint8Array): string {
    const decoded = this.decoder.push(chunk);
    if (decoded.length === 0) return "";
    return this.utf8.decode(toBytes(decoded), { stream: true });
  }

  /** Flush. Throws if the decoded stream ends mid UTF-8 sequence. */
  end(): string {
    const tail = this.decoder.end();
    let text = "";
    if (tail.length > 0) text += this.utf8.decode(toBytes(tail), { stream: true });
    text += this.utf8.decode();
    return text;
  }
}

function toBytes(decoded: DecodedByte[]): Uint8Array {
  const bytes = new Uint8Array(decoded.length);
  for (let i = 0; i < decoded.length; i++) bytes[i] = decoded[i].byte;
  return bytes;
}

export interface EncoderOptions {
  /**
   * When true, emit length-prefixed raw blocks (`\R<len>:`) instead of
   * per-byte escapes. Output is then not ASCII-safe but more compact.
   */
  useRawBlocks?: boolean;
  /** Chunk size for raw blocks; must match the decoder's limit. */
  maxRawLength?: number;
}

const HEX_CHARS = "0123456789ABCDEF";

/** Inverse of {@link decode}: encode arbitrary bytes into protocol text. */
export function encode(data: Uint8Array, options: EncoderOptions = {}): Uint8Array {
  const maxRaw = options.maxRawLength ?? DEFAULT_MAX_RAW_LENGTH;
  if (!Number.isInteger(maxRaw) || maxRaw < 1) {
    throw new RangeError(`maxRawLength must be a positive integer, got ${maxRaw}`);
  }
  const out: number[] = [];

  if (options.useRawBlocks) {
    for (let i = 0; i < data.length; i += maxRaw) {
      const end = Math.min(i + maxRaw, data.length);
      pushAscii(out, `\\R${end - i}:`);
      for (let j = i; j < end; j++) out.push(data[j]);
    }
    return Uint8Array.from(out);
  }

  for (const byte of data) {
    if (byte === BACKSLASH) {
      out.push(BACKSLASH, BACKSLASH);
    } else if (byte >= 0x20 && byte <= 0x7e) {
      out.push(byte);
    } else {
      out.push(BACKSLASH, 0x78, HEX_CHARS.charCodeAt(byte >> 4), HEX_CHARS.charCodeAt(byte & 0x0f));
    }
  }
  return Uint8Array.from(out);
}

function pushAscii(out: number[], text: string): void {
  for (let i = 0; i < text.length; i++) out.push(text.charCodeAt(i));
}
