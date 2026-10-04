/**
 * UTF-8 helpers.
 *
 * JavaScript strings are sequences of UTF-16 code units. A string is
 * encodable as UTF-8 exactly when it contains no lone surrogates, so
 * well-formedness checking reduces to surrogate-pair validation.
 * All offsets in this library count Unicode scalar values (code points),
 * never UTF-16 code units and never bytes, so edits can never split a
 * multi-byte encoding.
 */

export class InvalidEncodingError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "InvalidEncodingError";
  }
}

export function isWellFormedUtf8(text: string): boolean {
  for (let i = 0; i < text.length; i++) {
    const code = text.charCodeAt(i);
    if (code >= 0xd800 && code <= 0xdbff) {
      if (i + 1 >= text.length) return false;
      const next = text.charCodeAt(i + 1);
      if (next < 0xdc00 || next > 0xdfff) return false;
      i++;
    } else if (code >= 0xdc00 && code <= 0xdfff) {
      return false;
    }
  }
  return true;
}

export function assertWellFormedUtf8(text: string): void {
  if (!isWellFormedUtf8(text)) {
    throw new InvalidEncodingError(
      "text contains lone surrogate code units and is not encodable as UTF-8"
    );
  }
}

/** Number of Unicode scalar values. Only meaningful for well-formed text. */
export function countScalars(text: string): number {
  let count = 0;
  for (let i = 0; i < text.length; i++) {
    const code = text.charCodeAt(i);
    if (code >= 0xd800 && code <= 0xdbff) i++;
    count++;
  }
  return count;
}

/** Number of bytes when encoded as UTF-8. Only meaningful for well-formed text. */
export function utf8ByteLength(text: string): number {
  let bytes = 0;
  for (let i = 0; i < text.length; i++) {
    const code = text.charCodeAt(i);
    if (code <= 0x7f) {
      bytes += 1;
    } else if (code <= 0x7ff) {
      bytes += 2;
    } else if (code >= 0xd800 && code <= 0xdbff) {
      bytes += 4;
      i++;
    } else {
      bytes += 3;
    }
  }
  return bytes;
}

/**
 * Maps a scalar-value offset to a UTF-16 code-unit index.
 * Offset is clamped to [0, number of scalars in text].
 */
export function scalarOffsetToUtf16Index(text: string, offset: number): number {
  let scalars = 0;
  let i = 0;
  while (scalars < offset && i < text.length) {
    const code = text.charCodeAt(i);
    i += code >= 0xd800 && code <= 0xdbff ? 2 : 1;
    scalars++;
  }
  return i;
}
