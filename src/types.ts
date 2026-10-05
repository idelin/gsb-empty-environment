/**
 * One decoded output byte together with the half-open input range
 * [start, end) (absolute stream offsets) that produced it.
 */
export interface ByteSpan {
  /** Decoded byte value (0-255). */
  byte: number;
  /** Absolute input offset (inclusive) of this byte's source. */
  start: number;
  /** Absolute input offset (exclusive) of this byte's source. */
  end: number;
}

export interface DecoderOptions {
  /**
   * Maximum allowed raw-block payload length in bytes.
   * A `\r<len>:` header with len > maxRawLen is treated as an invalid
   * escape (the backslash is emitted literally and parsing resumes
   * after it). Defaults to 65535.
   */
  maxRawLen?: number;
}

export interface EncoderOptions {
  /**
   * Minimum run length of bytes that need escaping before the run is
   * emitted as a `\r<len>:` raw block instead of `\xHH` escapes.
   * Defaults to 4. Pass Infinity to disable raw blocks entirely.
   */
  rawThreshold?: number;
}
