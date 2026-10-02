/** Error kinds raised by the wire-format reader. */
export type WireErrorKind = "truncation" | "overflow" | "invalid-wire-type" | "invalid-tag";

/**
 * Raised whenever a read would cross the buffer boundary (truncated field),
 * a varint overflows 64 bits, or a tag/wire-type is malformed.
 * Carries the absolute offset so callers can locate the corrupt byte.
 */
export class WireError extends Error {
  readonly kind: WireErrorKind;
  readonly offset: number;

  constructor(kind: WireErrorKind, offset: number, message: string) {
    super(message);
    this.name = "WireError";
    this.kind = kind;
    this.offset = offset;
  }
}

export function truncation(offset: number, needed: number, remaining: number): WireError {
  return new WireError(
    "truncation",
    offset,
    `truncated buffer at offset ${offset}: need ${needed} byte(s), only ${remaining} remaining`,
  );
}
