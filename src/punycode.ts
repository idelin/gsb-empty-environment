const BASE = 36;
const T_MIN = 1;
const T_MAX = 26;
const SKEW = 38;
const DAMP = 700;
const INITIAL_BIAS = 72;
const INITIAL_N = 128;

function adapt(delta: number, pointCount: number, firstTime: boolean): number {
  let value = firstTime ? Math.floor(delta / DAMP) : Math.floor(delta / 2);
  value += Math.floor(value / pointCount);

  let offset = 0;
  while (value > Math.floor(((BASE - T_MIN) * T_MAX) / 2)) {
    value = Math.floor(value / (BASE - T_MIN));
    offset += BASE;
  }

  return offset + Math.floor(((BASE - T_MIN + 1) * value) / (value + SKEW));
}

function threshold(position: number, bias: number): number {
  if (position < T_MIN) {
    return T_MIN;
  }
  if (position < T_MAX) {
    return position;
  }
  return T_MAX;
}

function basicCodePointToDigit(codePoint: number): number {
  if (codePoint >= 0x30 && codePoint <= 0x39) {
    return codePoint - 22;
  }
  if (codePoint >= 0x41 && codePoint <= 0x5a) {
    return codePoint - 0x41;
  }
  if (codePoint >= 0x61 && codePoint <= 0x7a) {
    return codePoint - 0x61;
  }
  return BASE;
}

function digitToBasic(digit: number): string {
  return String.fromCharCode(digit < 26 ? digit + 0x61 : digit + 0x16);
}

/**
 * Encodes a Unicode label as a Punycode payload (without the `xn--` prefix).
 */
export function encodePunycode(input: string): string {
  const codePoints = Array.from(input, (character) => character.codePointAt(0) ?? 0);
  const basicCodePoints = codePoints.filter((codePoint) => codePoint < 0x80);
  const basicCount = basicCodePoints.length;

  let output = basicCodePoints.map((codePoint) => String.fromCharCode(codePoint)).join("");
  let handled = basicCount;
  if (basicCount > 0) {
    output += "-";
  }

  let current = INITIAL_N;
  let delta = 0;
  let bias = INITIAL_BIAS;

  while (handled < codePoints.length) {
    let next = 0x110000;
    for (const codePoint of codePoints) {
      if (codePoint >= current && codePoint < next) {
        next = codePoint;
      }
    }
    if (next === 0x110000) {
      throw new TypeError("Unable to find the next Punycode code point");
    }

    if (next - current > Number.MAX_SAFE_INTEGER / (handled + 1)) {
      throw new TypeError("Punycode integer overflow");
    }
    delta += (next - current) * (handled + 1);
    current = next;

    for (const codePoint of codePoints) {
      if (codePoint < current) {
        delta += 1;
        if (!Number.isSafeInteger(delta)) {
          throw new TypeError("Punycode integer overflow");
        }
      }
      if (codePoint !== current) {
        continue;
      }

      let quotient = delta;
      for (let position = BASE; ; position += BASE) {
        const boundary = threshold(position, bias);
        if (quotient < boundary) {
          break;
        }
        output += digitToBasic(boundary + ((quotient - boundary) % (BASE - boundary)));
        quotient = Math.floor((quotient - boundary) / (BASE - boundary));
      }

      output += digitToBasic(quotient);
      bias = adapt(delta, handled + 1, handled === basicCount);
      delta = 0;
      handled += 1;
    }

    delta += 1;
    current += 1;
  }

  return output;
}

/**
 * Decodes a Punycode payload (the part after `xn--`).
 * Throws on malformed input so callers can fail closed.
 */
export function decodePunycode(input: string): string {
  const output: number[] = [];
  let inputIndex = 0;
  let index = 0;
  let current = INITIAL_N;
  let bias = INITIAL_BIAS;

  const delimiter = input.lastIndexOf("-");
  if (delimiter >= 0) {
    for (let index = 0; index < delimiter; index += 1) {
      const codePoint = input.charCodeAt(index);
      if (codePoint >= 0x80) {
        throw new TypeError("Invalid Punycode basic code point");
      }
      output.push(codePoint);
    }
    inputIndex = delimiter + 1;
  }

  while (inputIndex < input.length) {
    const oldIndex = index;
    let weight = 1;

    for (let position = BASE; ; position += BASE) {
      if (inputIndex >= input.length) {
        throw new TypeError("Truncated Punycode payload");
      }

      const digit = basicCodePointToDigit(input.charCodeAt(inputIndex));
      inputIndex += 1;
      if (digit >= BASE) {
        throw new TypeError("Invalid Punycode digit");
      }
      if (digit > Math.floor((Number.MAX_SAFE_INTEGER - index) / weight)) {
        throw new TypeError("Punycode integer overflow");
      }

      index += digit * weight;
      const boundary = threshold(position, bias);
      if (digit < boundary) {
        break;
      }
      if (weight > Math.floor(Number.MAX_SAFE_INTEGER / (BASE - boundary))) {
        throw new TypeError("Punycode integer overflow");
      }
      weight *= BASE - boundary;
    }

    const nextPointCount = output.length + 1;
    bias = adapt(index - oldIndex, nextPointCount, oldIndex === 0);

    current += Math.floor(index / nextPointCount);
    if (current > 0x10ffff || (current >= 0xd800 && current <= 0xdfff)) {
      throw new TypeError("Invalid Unicode code point in Punycode payload");
    }

    const insertionIndex = index % nextPointCount;
    output.splice(insertionIndex, 0, current);
    index = insertionIndex + 1;
  }

  return output.map((codePoint) => String.fromCodePoint(codePoint)).join("");
}
