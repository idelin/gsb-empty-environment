export class SurrogatePairError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SurrogatePairError';
  }
}

export function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

export function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

export function splitsSurrogatePair(text: string, offset: number): boolean {
  return (
    offset > 0 &&
    offset < text.length &&
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset))
  );
}

export function assertUtf16Boundary(text: string, offset: number, what = 'offset'): void {
  if (!Number.isInteger(offset) || offset < 0 || offset > text.length) {
    throw new RangeError(what + ' ' + offset + ' out of range [0, ' + text.length + ']');
  }
  if (splitsSurrogatePair(text, offset)) {
    throw new SurrogatePairError(what + ' ' + offset + ' splits a UTF-16 surrogate pair');
  }
}
