import type { ByteSpan, DecoderOptions, EncoderOptions } from './types.js';

export const DEFAULT_MAX_RAW_LEN = 65535;

const BACKSLASH = 0x5c;
const CHAR_X = 0x78;
const CHAR_R = 0x72;
const COLON = 0x3a;
const ZERO = 0x30;

function isDigit(byte: number): boolean {
  return byte >= 0x30 && byte <= 0x39;
}

function isHexDigit(byte: number): boolean {
  return (
    (byte >= 0x30 && byte <= 0x39) ||
    (byte >= 0x41 && byte <= 0x46) ||
    (byte >= 0x61 && byte <= 0x66)
  );
}

function hexValue(byte: number): number {
  if (byte <= 0x39) return byte - 0x30;
  if (byte <= 0x46) return byte - 0x41 + 10;
  return byte - 0x61 + 10;
}

function hexChar(value: number): number {
  return value < 10 ? 0x30 + value : 0x61 + value - 10;
}

function isSafeByte(byte: number): boolean {
  return byte >= 0x20 && byte <= 0x7e && byte !== BACKSLASH;
}

/**
 * Incremental decoder for the escape protocol.
 *
 * Grammar (decoder is byte-transparent for anything not starting a
 * valid escape):
 *   escape := "\\"                  -> literal backslash
 *           | "\x" 2HEXDIG          -> arbitrary byte
 *           | "\r" 1*DIGIT ":" raw  -> <len> raw bytes, backslash not special
 *
 * Invalid escapes emit the backslash literally and reprocess the
 * following byte, so a bad escape never swallows later valid input.
 * Incomplete escapes at end-of-stream are emitted literally by end().
 */
export class StreamDecoder {
  private maxRawLen: number;
  private buf: number[];
  private base: number;
  private ended: boolean;

  constructor(options: DecoderOptions = {}) {
    const maxRawLen = options.maxRawLen === undefined ? DEFAULT_MAX_RAW_LEN : options.maxRawLen;
    if (!Number.isInteger(maxRawLen) || maxRawLen < 0) {
      throw new RangeError('maxRawLen must be a non-negative integer');
    }
    this.maxRawLen = maxRawLen;
    this.buf = [];
    this.base = 0;
    this.ended = false;
  }

  /** Feed the next chunk of input; returns spans decoded so far. */
  push(data: Uint8Array): ByteSpan[] {
    if (this.ended) throw new Error('push() called after end()');
    for (let i = 0; i < data.length; i += 1) this.buf.push(data[i]);
    return this.drain(false);
  }

  /** Signal end-of-stream; leftover incomplete escapes pass through literally. */
  end(): ByteSpan[] {
    this.ended = true;
    return this.drain(true);
  }

  private drain(flush: boolean): ByteSpan[] {
    const out: ByteSpan[] = [];
    const buf = this.buf;
    const base = this.base;
    const maxRawLen = this.maxRawLen;
    const n = buf.length;
    let pos = 0;
    while (pos < n) {
      const byte = buf[pos];
      if (byte !== BACKSLASH) {
        out.push({ byte, start: base + pos, end: base + pos + 1 });
        pos += 1;
        continue;
      }
      if (pos + 1 >= n) break;
      const c = buf[pos + 1];
      if (c === BACKSLASH) {
        out.push({ byte: BACKSLASH, start: base + pos, end: base + pos + 2 });
        pos += 2;
        continue;
      }
      if (c === CHAR_X) {
        if (pos + 3 >= n) break;
        const hi = buf[pos + 2];
        const lo = buf[pos + 3];
        if (isHexDigit(hi) && isHexDigit(lo)) {
          out.push({ byte: hexValue(hi) * 16 + hexValue(lo), start: base + pos, end: base + pos + 4 });
          pos += 4;
        } else {
          out.push({ byte: BACKSLASH, start: base + pos, end: base + pos + 1 });
          pos += 1;
        }
        continue;
      }
      if (c === CHAR_R) {
        let j = pos + 2;
        let len = 0;
        let digits = 0;
        let bad = false;
        while (j < n && isDigit(buf[j])) {
          len = len * 10 + (buf[j] - ZERO);
          digits += 1;
          j += 1;
          if (len > maxRawLen) {
            bad = true;
            break;
          }
        }
        if (!bad && j < n && (digits === 0 || buf[j] !== COLON)) bad = true;
        if (bad) {
          out.push({ byte: BACKSLASH, start: base + pos, end: base + pos + 1 });
          pos += 1;
          continue;
        }
        if (j >= n) break;
        const dataStart = j + 1;
        if (dataStart + len > n) break;
        for (let k = 0; k < len; k += 1) {
          out.push({ byte: buf[dataStart + k], start: base + dataStart + k, end: base + dataStart + k + 1 });
        }
        pos = dataStart + len;
        continue;
      }
      out.push({ byte: BACKSLASH, start: base + pos, end: base + pos + 1 });
      pos += 1;
    }
    this.buf = buf.slice(pos);
    this.base = base + pos;
    if (flush) {
      const rest = this.buf;
      const restBase = this.base;
      for (let k = 0; k < rest.length; k += 1) {
        out.push({ byte: rest[k], start: restBase + k, end: restBase + k + 1 });
      }
      this.buf = [];
      this.base = restBase + rest.length;
    }
    return out;
  }
}

/** One-shot decode: returns spans for the whole input. */
export function decode(data: Uint8Array, options: DecoderOptions = {}): ByteSpan[] {
  const decoder = new StreamDecoder(options);
  const out = decoder.push(data);
  return out.concat(decoder.end());
}

/** One-shot decode to raw bytes (spans discarded). */
export function decodeBytes(data: Uint8Array, options: DecoderOptions = {}): Uint8Array {
  const spans = decode(data, options);
  const out = new Uint8Array(spans.length);
  for (let i = 0; i < spans.length; i += 1) out[i] = spans[i].byte;
  return out;
}

/**
 * Decode and validate the result as UTF-8. Validation happens strictly
 * after decoding: the protocol input itself is treated as bytes, so a
 * multi-byte UTF-8 sequence may be assembled from several escapes or
 * raw blocks. Throws a TypeError on malformed UTF-8.
 */
export function decodeUtf8(data: Uint8Array, options: DecoderOptions = {}): string {
  return new TextDecoder('utf-8', { fatal: true }).decode(decodeBytes(data, options));
}

/**
 * Inverse of decode: encode arbitrary bytes into protocol text.
 * Safe printable bytes pass through, backslash becomes "\\", other
 * bytes become "\xHH" or, for runs of at least rawThreshold, a
 * "\r<len>:" raw block. Guarantees decode(encode(x)) == x.
 */
export function encode(data: Uint8Array, options: EncoderOptions = {}): Uint8Array {
  const rawThreshold = options.rawThreshold === undefined ? 4 : options.rawThreshold;
  const out: number[] = [];
  const n = data.length;
  let i = 0;
  while (i < n) {
    const byte = data[i];
    if (isSafeByte(byte)) {
      out.push(byte);
      i += 1;
      continue;
    }
    if (byte === BACKSLASH) {
      out.push(BACKSLASH, BACKSLASH);
      i += 1;
      continue;
    }
    let j = i;
    while (j < n && !isSafeByte(data[j]) && data[j] !== BACKSLASH) j += 1;
    const runLen = j - i;
    if (runLen >= rawThreshold) {
      const header = '\\r' + runLen + ':';
      for (let k = 0; k < header.length; k += 1) out.push(header.charCodeAt(k));
      for (let k = i; k < j; k += 1) out.push(data[k]);
    } else {
      for (let k = i; k < j; k += 1) {
        out.push(BACKSLASH, CHAR_X, hexChar(data[k] >> 4), hexChar(data[k] & 15));
      }
    }
    i = j;
  }
  return Uint8Array.from(out);
}

/** Encode a string as UTF-8 protocol text. */
export function encodeUtf8(text: string, options: EncoderOptions = {}): Uint8Array {
  return encode(new TextEncoder().encode(text), options);
}
