import type { Scalar, ScalarSpan } from "./types.ts";

export function isUnicodeScalar(value: number): boolean {
  return (
    Number.isInteger(value) &&
    value >= 0 &&
    value <= 0x10ffff &&
    !(value >= 0xd800 && value <= 0xdfff)
  );
}

export function assertUnicodeScalar(value: number, context: string): void {
  if (!isUnicodeScalar(value)) {
    throw new TypeError(
      `${context}: ${String(value)} is not a Unicode scalar value`,
    );
  }
}

export function isHighSurrogate(codeUnit: number): boolean {
  return codeUnit >= 0xd800 && codeUnit <= 0xdbff;
}

export function isLowSurrogate(codeUnit: number): boolean {
  return codeUnit >= 0xdc00 && codeUnit <= 0xdfff;
}

export function assertWellFormedUtf16(text: string, context = "text"): void {
  for (let i = 0; i < text.length; i++) {
    const unit = text.charCodeAt(i);
    if (isHighSurrogate(unit)) {
      if (i + 1 >= text.length || !isLowSurrogate(text.charCodeAt(i + 1))) {
        throw new TypeError(`${context}: lone high surrogate at UTF-16 ${i}`);
      }
      i++;
    } else if (isLowSurrogate(unit)) {
      throw new TypeError(`${context}: lone low surrogate at UTF-16 ${i}`);
    }
  }
}

/**
 * A UTF-16 boundary is invalid when it falls between a high and a low
 * surrogate.  Display segments must never end or start at such a boundary.
 */
export function isBoundaryInsideSurrogatePair(
  text: string,
  position: number,
): boolean {
  return (
    position > 0 &&
    position < text.length &&
    isHighSurrogate(text.charCodeAt(position - 1)) &&
    isLowSurrogate(text.charCodeAt(position))
  );
}

export function codePoints(text: string): Scalar[] {
  assertWellFormedUtf16(text);
  return Array.from(text, (character) => character.codePointAt(0) as number);
}

/**
 * Map each Unicode scalar to its exact UTF-16 half-open range.  Iterating with
 * `for..of` keeps surrogate pairs together.
 */
export function scalarSpans(text: string, baseOffset = 0): ScalarSpan[] {
  assertWellFormedUtf16(text);
  const spans: ScalarSpan[] = [];
  let offset = 0;
  for (const character of text) {
    const codePoint = character.codePointAt(0) as number;
    const width = character.length;
    spans.push({
      codePoint,
      utf16Start: baseOffset + offset,
      utf16End: baseOffset + offset + width,
    });
    offset += width;
  }
  return spans;
}

export function hasNonAscii(text: string): boolean {
  for (const character of text) {
    if ((character.codePointAt(0) as number) > 0x7f) return true;
  }
  return false;
}

export function splitDomainText(domain: string): string[] {
  let text = domain.trim();
  if (text.endsWith(".")) text = text.slice(0, -1);
  if (text.length === 0) return [];
  return text.split(".");
}

export function normalizeLabelKey(label: string): string {
  return label.normalize("NFC").toLowerCase();
}

export function normalizeDomainKey(domain: string): string {
  return splitDomainText(domain).map(normalizeLabelKey).join(".");
}

export function normalizeSuffix(suffix: string): string {
  const trimmed = suffix.trim().replace(/^\.+/, "").replace(/\.+$/, "");
  return trimmed === "*" ? "*" : normalizeDomainKey(trimmed);
}

/**
 * DNS names are ASCII case-insensitive.  This helper folds only ASCII letters;
 * the host is expected to pass IDNA-normalised labels.
 */
export function foldAsciiCase(scalar: Scalar): Scalar {
  return scalar >= 0x41 && scalar <= 0x5a ? scalar + 0x20 : scalar;
}

export function domainScalars(domain: string): Scalar[] {
  return codePoints(normalizeDomainKey(domain));
}

const BASE = 36;
const TMIN = 1;
const TMAX = 26;
const SKEW = 38;
const DAMP = 700;
const INITIAL_BIAS = 72;
const INITIAL_N = 128;

function digitToBasic(digit: number): number {
  return digit + 22 + 75 * (digit < 26 ? 1 : 0);
}

function adaptBias(delta: number, numPoints: number, firstTime: boolean): number {
  let value = firstTime ? Math.floor(delta / DAMP) : delta >> 1;
  value += Math.floor(value / numPoints);
  let k = 0;
  while (value > (((BASE - TMIN) * TMAX) >> 1)) {
    value = Math.floor(value / (BASE - TMIN));
    k += BASE;
  }
  return k + Math.floor(((BASE - TMIN + 1) * value) / (value + SKEW));
}

/**
 * RFC 3492 punycode encoder for an already IDNA-mapped label.  The output is
 * ASCII and therefore cannot split a surrogate pair.
 */
export function punycodeEncode(label: string): string {
  const input = codePoints(label);
  const output: number[] = [];

  for (const scalar of input) {
    if (scalar < 0x80) output.push(scalar);
  }

  const basicLength = output.length;
  let handled = basicLength;
  if (basicLength > 0) output.push(0x2d);

  let n = INITIAL_N;
  let delta = 0;
  let bias = INITIAL_BIAS;

  while (handled < input.length) {
    let next = Number.POSITIVE_INFINITY;
    for (const scalar of input) {
      if (scalar >= n && scalar < next) next = scalar;
    }
    if (!Number.isFinite(next)) {
      throw new Error("punycode: no next code point");
    }

    const step = (next - n) * (handled + 1);
    if (!Number.isSafeInteger(step) || step < 0) {
      throw new Error("punycode: overflow");
    }
    delta += step;
    if (!Number.isSafeInteger(delta) || delta < 0) {
      throw new Error("punycode: overflow");
    }
    n = next;

    for (const scalar of input) {
      if (scalar < n) {
        delta += 1;
        if (!Number.isSafeInteger(delta) || delta < 0) {
          throw new Error("punycode: overflow");
        }
      } else if (scalar === n) {
        let q = delta;
        for (let k = BASE; ; k += BASE) {
          const threshold =
            k <= bias ? TMIN : k >= bias + TMAX ? TMAX : k - bias;
          if (q < threshold) break;
          const code =
            threshold + ((q - threshold) % (BASE - threshold));
          output.push(digitToBasic(code));
          q = Math.floor((q - threshold) / (BASE - threshold));
        }
        output.push(digitToBasic(q));
        bias = adaptBias(delta, handled + 1, handled === basicLength);
        delta = 0;
        handled += 1;
      }
    }

    delta += 1;
    n += 1;
  }

  return String.fromCharCode(...output);
}

/**
 * ASCII (A-label) form of one IDNA-validated label.  ASCII labels are
 * lower-cased; non-ASCII labels become `xn--` plus RFC 3492 punycode.
 */
export function toAsciiLabel(label: string): string {
  if (label.includes(".")) {
    return splitDomainText(label).map(toAsciiLabel).join(".");
  }
  const normalized = label.normalize("NFC").toLowerCase();
  if (!hasNonAscii(normalized)) return normalized;
  return `xn--${punycodeEncode(normalized)}`;
}
