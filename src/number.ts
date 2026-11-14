const JSON_NUMBER_PATTERN =
  /^(-?)(0|[1-9]\d*)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/;

/**
 * Produces a deterministic, precision-preserving representation of a JSON
 * number token.
 *
 * The mantissa keeps every significant digit and the decimal exponent is kept
 * symbolic, so values such as 9007199254740993 and 1e400 never pass through an
 * IEEE-754 double. Numerically equal spellings (100, 1e2, 100.0) collapse to
 * the same canonical token.
 */
export function canonicalizeNumber(raw: string): string {
  const match = JSON_NUMBER_PATTERN.exec(raw);
  if (match === null) {
    throw new TypeError(`Invalid JSON number token: ${raw}`);
  }

  const negative = match[1] === "-";
  const integerPart = match[2];
  const fractionPart = match[3] ?? "";
  const exponent = BigInt(match[4] ?? "0");

  let digits = (integerPart + fractionPart).replace(/^0+/, "");
  if (digits.length === 0) {
    return "0";
  }

  let decimalExponent = exponent - BigInt(fractionPart.length);
  let end = digits.length;
  while (end > 1 && digits.charCodeAt(end - 1) === 0x30) {
    end -= 1;
    decimalExponent += 1n;
  }
  digits = digits.slice(0, end);

  const scientificExponent =
    decimalExponent + BigInt(digits.length - 1);
  const mantissa =
    digits.length === 1
      ? digits
      : `${digits[0]}.${digits.slice(1)}`;
  const sign = negative ? "-" : "";
  const exponentSuffix =
    scientificExponent === 0n ? "" : `e${scientificExponent.toString()}`;

  return `${sign}${mantissa}${exponentSuffix}`;
}

/**
 * Returns a JS number only when converting to a double and back is exactly
 * equivalent to the original token. Unsafe values are returned as their exact
 * source spelling instead.
 */
export function exactNumberToSafeValue(
  raw: string,
  canonical: string,
): number | string {
  const value = Number(raw);
  if (!Number.isFinite(value)) {
    return raw;
  }

  if (Number.isInteger(value)) {
    if (!Number.isSafeInteger(value)) {
      return raw;
    }
    try {
      return canonicalizeNumber(String(value)) === canonical ? value : raw;
    } catch {
      return raw;
    }
  }

  try {
    if (canonicalizeNumber(String(value)) === canonical) {
      return value;
    }
  } catch {
    return raw;
  }
  return raw;
}
