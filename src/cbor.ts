import { AuthenticatorDataError } from "./errors.ts";

const MAJOR_UINT = 0;
const MAJOR_NINT = 1;
const MAJOR_BYTES = 2;
const MAJOR_TEXT = 3;
const MAJOR_ARRAY = 4;
const MAJOR_MAP = 5;
const MAJOR_TAG = 6;
const MAJOR_SIMPLE = 7;

const BREAK = 0xff;

function readHead(bytes: Uint8Array, offset: number): { major: number; value: number; next: number } {
  if (offset >= bytes.length) {
    throw new AuthenticatorDataError("CBOR data is truncated");
  }
  const initial = bytes[offset];
  const major = initial >> 5;
  const info = initial & 0x1f;
  let next = offset + 1;

  if (info < 24) {
    return { major, value: info, next };
  }
  if (info === 24) {
    if (next >= bytes.length) {
      throw new AuthenticatorDataError("CBOR data is truncated");
    }
    return { major, value: bytes[next], next: next + 1 };
  }
  if (info === 25) {
    if (next + 2 > bytes.length) {
      throw new AuthenticatorDataError("CBOR data is truncated");
    }
    const value = (bytes[next] << 8) | bytes[next + 1];
    return { major, value, next: next + 2 };
  }
  if (info === 26) {
    if (next + 4 > bytes.length) {
      throw new AuthenticatorDataError("CBOR data is truncated");
    }
    const value =
      ((bytes[next] << 24) >>> 0) +
      (bytes[next + 1] << 16) +
      (bytes[next + 2] << 8) +
      bytes[next + 3];
    return { major, value, next: next + 4 };
  }
  if (info === 27) {
    if (next + 8 > bytes.length) {
      throw new AuthenticatorDataError("CBOR data is truncated");
    }
    const high =
      ((bytes[next] << 24) >>> 0) +
      (bytes[next + 1] << 16) +
      (bytes[next + 2] << 8) +
      bytes[next + 3];
    const low =
      ((bytes[next + 4] << 24) >>> 0) +
      (bytes[next + 5] << 16) +
      (bytes[next + 6] << 8) +
      bytes[next + 7];
    const value = high * 0x100000000 + low;
    if (!Number.isSafeInteger(value)) {
      throw new AuthenticatorDataError("CBOR integer exceeds safe integer range");
    }
    return { major, value, next: next + 8 };
  }
  if (info === 31) {
    throw new AuthenticatorDataError("Indefinite-length CBOR items are not allowed in authenticator data");
  }
  throw new AuthenticatorDataError("Unsupported CBOR additional information");
}

/**
 * Compute the total encoded length of one definite-length CBOR item starting
 * at `offset`. Indefinite-length items and floating point values are rejected
 * because authenticator data extensions use a strict definite-length subset.
 */
export function cborItemLength(bytes: Uint8Array, offset: number): number {
  let cursor = offset;
  const head = readHead(bytes, cursor);
  cursor = head.next;

  switch (head.major) {
    case MAJOR_UINT:
    case MAJOR_NINT:
    case MAJOR_SIMPLE:
      if (head.value === BREAK) {
        throw new AuthenticatorDataError("Unexpected CBOR break code");
      }
      return cursor - offset;
    case MAJOR_BYTES:
    case MAJOR_TEXT:
      if (cursor + head.value > bytes.length) {
        throw new AuthenticatorDataError("CBOR string payload is truncated");
      }
      cursor += head.value;
      return cursor - offset;
    case MAJOR_TAG:
      cursor += cborItemLength(bytes, cursor);
      return cursor - offset;
    case MAJOR_ARRAY: {
      for (let index = 0; index < head.value; index += 1) {
        cursor += cborItemLength(bytes, cursor);
      }
      return cursor - offset;
    }
    case MAJOR_MAP: {
      for (let index = 0; index < head.value; index += 1) {
        cursor += cborItemLength(bytes, cursor);
        cursor += cborItemLength(bytes, cursor);
      }
      return cursor - offset;
    }
    default:
      throw new AuthenticatorDataError("Unsupported CBOR major type");
  }
}
