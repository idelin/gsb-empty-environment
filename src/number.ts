import type {
  AmountIssue,
  AmountScanResult,
  InvalidAmountToken,
  NumberFormatRule,
  ParsedAmount,
  PriceTextParseResult,
  PricingRules,
} from './types.ts';

export interface NormalizedNumberFormat {
  decimal: string | null;
  grouping: string[];
  groupingConfigured: boolean;
  groupingSize: number;
  /** Sizes ordered from the rightmost group to the leftmost. */
  groupingSizes: number[];
  maxFractionDigits?: number;
  allowNegative: boolean;
  allowLeadingPlus: boolean;
}

export interface NumberFormatIssue {
  code: string;
  severity: 'error' | 'warning';
  message: string;
}

export interface NormalizedNumberFormatResult {
  format: NormalizedNumberFormat;
  issues: NumberFormatIssue[];
}

const GENERIC_SEPARATORS = new Set(['.', ',', '·', '٬', '٫']);

const DIGIT_ZERO_CODES = [
  0x0030,
  0x0660,
  0x06f0,
  0x0966,
  0x09e6,
  0x0a66,
  0x0ae6,
  0x0b66,
  0x0be6,
  0x0c66,
  0x0ce6,
  0x0d66,
  0x0e50,
  0x0ed0,
  0x1040,
  0x17e0,
  0x1810,
  0xff10,
];

function digitValue(char: string): number | null {
  if (char.length !== 1) return null;
  const code = char.charCodeAt(0);
  if (code >= 0x30 && code <= 0x39) return code - 0x30;
  for (const zero of DIGIT_ZERO_CODES) {
    if (code >= zero && code <= zero + 9) return code - zero;
  }
  return null;
}

function isDigit(char: string): boolean {
  return digitValue(char) !== null;
}

function normalizeDigitString(value: string): string {
  let result = '';
  for (const char of value) {
    const digit = digitValue(char);
    if (digit === null) return value;
    result += String(digit);
  }
  return result;
}

function firstDefined<T>(...values: Array<T | undefined>): T | undefined {
  for (const value of values) {
    if (value !== undefined) return value;
  }
  return undefined;
}

function normalizeGrouping(value: string | string[] | null | undefined): string[] {
  if (value === null || value === undefined) return [];
  if (Array.isArray(value)) {
    return value
      .flatMap((entry) => entry.split(''))
      .filter((char) => char.length === 1);
  }
  if (value === '' || value.toLowerCase() === 'none' || value.toLowerCase() === 'null') return [];
  return value.split('').filter((char) => char.length === 1);
}

export function normalizeNumberFormat(
  rules: PricingRules | NumberFormatRule | undefined,
  extraIssues: NumberFormatIssue[] = [],
): NormalizedNumberFormatResult {
  const issues = extraIssues;
  const rule = (rules ?? {}) as PricingRules & NumberFormatRule;
  const nested = rule.numberFormat ?? rule.format ?? {};

  const decimal = firstDefined(
    nested.decimal,
    nested.decimalSeparator,
    nested.decimalPoint,
    nested.decimalChar,
    nested.radix,
    rule.decimal,
    rule.decimalSeparator,
    rule.decimalChar,
  );
  const groupingRaw = firstDefined(
    nested.grouping,
    nested.groupingSeparator,
    nested.groupSeparator,
    nested.groupChar,
    nested.thousands,
    nested.thousandsSeparator,
    rule.grouping,
    rule.groupingSeparator,
    rule.groupChar,
    rule.thousandSeparator,
  );
  const groupingConfigured = groupingRaw !== undefined;
  let grouping = normalizeGrouping(groupingRaw);
  if (grouping.some((char) => /\s/.test(char))) {
    grouping = Array.from(new Set([...grouping, ' ', '\u00a0', '\u202f', '\u2009']));
  }

  if (decimal === undefined || decimal === null || decimal === '') {
    issues.push({
      code: 'config-missing-decimal-separator',
      severity: 'error',
      message: 'Site config must explicitly declare decimal (for example decimal: ".").',
    });
  } else if (decimal.length !== 1) {
    issues.push({
      code: 'config-invalid-decimal-separator',
      severity: 'error',
      message: `Decimal separator must be a single character, received "${decimal}".`,
    });
  } else if (grouping.includes(decimal)) {
    issues.push({
      code: 'config-ambiguous-separators',
      severity: 'error',
      message: `Decimal and grouping separators must differ; both are "${decimal}".`,
    });
  }
  if (!groupingConfigured) {
    issues.push({
      code: 'config-missing-grouping-separator',
      severity: 'error',
      message: 'Site config must explicitly declare grouping (use grouping: null when the site has no grouping separator).',
    });
  }
  if (grouping.some((char) => char.length !== 1)) {
    issues.push({
      code: 'config-invalid-grouping-separator',
      severity: 'error',
      message: 'Grouping separators must each be a single character.',
    });
  }

  const groupingSize = firstDefined(
    nested.groupingSize,
    rule.groupingSize,
  ) ?? 3;
  let groupingSizes = nested.groupingSizes ?? rule.groupingSizes ?? [groupingSize];
  if (!Array.isArray(groupingSizes) || groupingSizes.length === 0) groupingSizes = [groupingSize];
  groupingSizes = groupingSizes.map((size) => Math.max(0, Math.trunc(size)));
  if (groupingSizes.some((size) => size <= 0)) {
    issues.push({
      code: 'config-invalid-grouping-size',
      severity: 'error',
      message: 'Grouping sizes must be positive integers.',
    });
    groupingSizes = [groupingSize > 0 ? groupingSize : 3];
  }

  const format: NormalizedNumberFormat = {
    decimal: typeof decimal === 'string' && decimal.length === 1 ? decimal : null,
    grouping,
    groupingConfigured,
    groupingSize,
    groupingSizes,
    maxFractionDigits: firstDefined(nested.maxFractionDigits, rule.numberFormat?.maxFractionDigits),
    allowNegative: Boolean(firstDefined(nested.allowNegative, rule.numberFormat?.allowNegative) ?? false),
    allowLeadingPlus: Boolean(firstDefined(nested.allowLeadingPlus, rule.numberFormat?.allowLeadingPlus) ?? false),
  };

  return { format, issues };
}

function isSeparator(char: string, format: NormalizedNumberFormat): boolean {
  if (format.decimal === char) return true;
  if (format.grouping.includes(char)) return true;
  if (GENERIC_SEPARATORS.has(char)) return true;
  if (format.grouping.some((group) => /\s/.test(group)) && /\s/.test(char)) return true;
  return false;
}

function makeIssue(code: string, message: string, start: number, end: number, text: string): AmountIssue {
  return { code, message, start, end, text };
}

function validateGroups(
  integerPart: string,
  format: NormalizedNumberFormat,
  start: number,
  end: number,
  raw: string,
): AmountIssue | null {
  const usedGroups = format.grouping.filter((char) => integerPart.includes(char));
  if (!usedGroups.length) {
    if (GENERIC_SEPARATORS.has('.') || GENERIC_SEPARATORS.has(',')) {
      for (const char of ['.', ',', '·', '٬', '٫']) {
        if (integerPart.includes(char) && !format.grouping.includes(char)) {
          return makeIssue(
            'unconfigured-grouping-separator',
            `Grouping separator "${char}" is not declared in the site config.`,
            start,
            end,
            raw,
          );
        }
      }
    }
    return null;
  }
  if (usedGroups.length > 1) {
    return makeIssue(
      'mixed-grouping-separators',
      `Multiple grouping separators (${usedGroups.join(', ')}) are used in one amount.`,
      start,
      end,
      raw,
    );
  }

  const groupChar = usedGroups[0];
  const groups = integerPart.split(groupChar);
  if (!groups.length || groups[0] === '' || groups.some((group) => group === '')) {
    return makeIssue('malformed-grouping', 'A grouping separator is adjacent to another separator or an amount boundary.', start, end, raw);
  }

  const sizes = format.groupingSizes.length ? format.groupingSizes : [format.groupingSize];
  for (let index = 1; index < groups.length; index += 1) {
    const fromRight = groups.length - 1 - index;
    const expected = sizes[Math.min(fromRight, sizes.length - 1)];
    if (groups[index].length !== expected) {
      return makeIssue(
        'invalid-group-size',
        `Group "${groups[index]}" has ${groups[index].length} digits; site config expects ${expected}.`,
        start,
        end,
        raw,
      );
    }
  }
  const firstExpected = sizes[Math.min(groups.length - 1, sizes.length - 1)];
  if (groups[0].length > firstExpected) {
    return makeIssue(
      'invalid-group-size',
      `Leading group has ${groups[0].length} digits; site config permits at most ${firstExpected}.`,
      start,
      end,
      raw,
    );
  }
  return null;
}

function parseToken(
  raw: string,
  start: number,
  format: NormalizedNumberFormat,
): { amount?: ParsedAmount; issue?: AmountIssue } {
  let sign = '';
  let body = raw;
  if (body.startsWith('-') || body.startsWith('+')) {
    sign = body[0];
    body = body.slice(1);
  }
  if (sign === '-' && !format.allowNegative) {
    return {
      issue: makeIssue('negative-amount', 'Negative amounts are not accepted as a current price.', start, start + raw.length, raw),
    };
  }
  if (sign === '+' && !format.allowLeadingPlus) {
    return {
      issue: makeIssue('leading-plus', 'A leading plus sign is not accepted by the site config.', start, start + raw.length, raw),
    };
  }
  if (!body.length) {
    return { issue: makeIssue('empty-amount', 'No digits are present in the amount token.', start, start + raw.length, raw) };
  }

  const decimalCount = format.decimal ? body.split(format.decimal).length - 1 : 0;
  if (decimalCount > 1) {
    return {
      issue: makeIssue('multiple-decimal-separators', 'An amount may contain at most one decimal separator.', start, start + raw.length, raw),
    };
  }

  let decimalIndex = -1;
  if (format.decimal && decimalCount === 1) decimalIndex = body.indexOf(format.decimal);

  for (const char of body) {
    if (isDigit(char)) continue;
    if (format.decimal && char === format.decimal) continue;
    if (format.grouping.includes(char)) continue;
    if (GENERIC_SEPARATORS.has(char)) {
      return {
        issue: makeIssue(
          decimalIndex >= 0 && body.indexOf(char) > decimalIndex ? 'separator-after-decimal' : 'unconfigured-separator',
          `Separator "${char}" is not valid under the configured decimal/grouping rules.`,
          start,
          start + raw.length,
          raw,
        ),
      };
    }
    return {
      issue: makeIssue('non-numeric-character', `Unexpected character "${char}" in amount.`, start, start + raw.length, raw),
    };
  }

  if (!format.decimal && body.split('').some((char) => GENERIC_SEPARATORS.has(char))) {
    return {
      issue: makeIssue(
        'decimal-separator-not-configured',
        'The amount contains a separator but the site config does not declare a decimal separator.',
        start,
        start + raw.length,
        raw,
      ),
    };
  }

  const integerPartRaw = decimalIndex >= 0 ? body.slice(0, decimalIndex) : body;
  const fractionPartRaw = decimalIndex >= 0 ? body.slice(decimalIndex + 1) : '';
  if (decimalIndex >= 0 && !fractionPartRaw.length) {
    return {
      issue: makeIssue('missing-fraction-digits', 'The decimal separator is not followed by digits.', start, start + raw.length, raw),
    };
  }

  const integerDigitsRaw = format.grouping.length
    ? Array.from(integerPartRaw).filter((char) => !format.grouping.includes(char)).join('')
    : integerPartRaw;
  let integerPart = normalizeDigitString(integerDigitsRaw);
  const fractionPart = normalizeDigitString(fractionPartRaw);
  if (!integerPart.length && !fractionPart.length) {
    return { issue: makeIssue('empty-amount', 'The amount contains no digits.', start, start + raw.length, raw) };
  }
  if (decimalIndex >= 0 && !/^\d+$/.test(fractionPart)) {
    return {
      issue: makeIssue('non-digit-fraction', 'The fractional part must contain digits only.', start, start + raw.length, raw),
    };
  }
  if (format.maxFractionDigits !== undefined && fractionPart.length > format.maxFractionDigits) {
    return {
      issue: makeIssue(
        'too-many-fraction-digits',
        `Fraction has ${fractionPart.length} digits; config permits ${format.maxFractionDigits}.`,
        start,
        start + raw.length,
        raw,
      ),
    };
  }

  const groupIssue = validateGroups(integerPartRaw, format, start, start + raw.length, raw);
  if (groupIssue) return { issue: groupIssue };

  if (!integerPart.length && decimalIndex === 0 && fractionPart.length) {
    integerPart = '0';
  }
  if (!integerPart.length) {
    return {
      issue: makeIssue('missing-integer-digits', 'An amount must contain at least one integer digit.', start, start + raw.length, raw),
    };
  }

  const numericText = `${integerPart}.${fractionPart || '0'}`;
  const value = Number(numericText);
  if (!Number.isFinite(value)) {
    return {
      issue: makeIssue('amount-out-of-range', 'The parsed amount is not a finite number.', start, start + raw.length, raw),
    };
  }

  return {
    amount: {
      text: raw,
      start,
      end: start + raw.length,
      value,
      integerDigits: integerPart,
      fractionDigits: fractionPart,
      usedGrouping: format.grouping.some((char) => integerPartRaw.includes(char)),
      usedDecimal: decimalIndex >= 0,
    },
  };
}

export function scanAmountText(
  text: string,
  format: NormalizedNumberFormat,
  baseOffset = 0,
): AmountScanResult {
  const amounts: ParsedAmount[] = [];
  const invalid: InvalidAmountToken[] = [];
  let index = 0;

  while (index < text.length) {
    const startsLeadingDecimal =
      format.decimal !== null &&
      text[index] === format.decimal &&
      index + 1 < text.length &&
      isDigit(text[index + 1]) &&
      (index === 0 || (!isDigit(text[index - 1]) && !isSeparator(text[index - 1], format)));
    if (!isDigit(text[index]) && !startsLeadingDecimal) {
      index += 1;
      continue;
    }

    let start = index;
    if (index > 0 && (text[index - 1] === '-' || text[index - 1] === '+')) {
      const beforeSign = index - 2;
      if (beforeSign < 0 || (!isDigit(text[beforeSign]) && !isSeparator(text[beforeSign], format))) {
        start = index - 1;
      }
    }

    let cursor = index;
    let lastDigit = index;
    while (cursor < text.length) {
      const char = text[cursor];
      if (isDigit(char)) {
        lastDigit = cursor;
        cursor += 1;
        continue;
      }
      if (isSeparator(char, format)) {
        cursor += 1;
        continue;
      }
      break;
    }
    const end = lastDigit + 1;
    const raw = text.slice(start, Math.max(end, index + 1));
    const parsed = parseToken(raw, tick(start, baseOffset), format);
    if (parsed.amount) {
      amounts.push(parsed.amount);
    } else if (parsed.issue) {
      invalid.push({
        text: raw,
        start: parsed.issue.start,
        end: parsed.issue.end,
        issue: parsed.issue,
      });
    }
    index = Math.max(cursor, index + 1);
  }

  return { amounts, invalid };
}

function tick(value: number, baseOffset: number): number {
  return value + baseOffset;
}

export function parsePriceText(
  text: string,
  rules?: PricingRules | NumberFormatRule,
): PriceTextParseResult {
  const { format } = normalizeNumberFormat(rules);
  const scan = scanAmountText(text, format);
  return {
    amounts: scan.amounts,
    invalid: scan.invalid,
    ambiguous: scan.amounts.length > 1,
  };
}

export function formatPriceValue(value: number, format: NormalizedNumberFormat): string {
  const decimal = format.decimal ?? '.';
  const group = format.grouping[0] ?? '';
  const fixed = Number.isInteger(value) ? String(value) : value.toFixed(Math.max(2, format.maxFractionDigits ?? 2));
  const [integer, fraction = ''] = fixed.split('.');
  let grouped = integer;
  if (group && format.groupingSizes.length) {
    const size = format.groupingSizes[0];
    const parts: string[] = [];
    let rest = integer;
    while (rest.length > size) {
      parts.unshift(rest.slice(-size));
      rest = rest.slice(0, -size);
    }
    parts.unshift(rest);
    grouped = parts.join(group);
  }
  return fraction ? `${grouped}${decimal}${fraction}` : grouped;
}

export const parsePrice = parsePriceText;
export const parseAmountText = parsePriceText;
export const parseAmount = parsePriceText;
export const scanAmounts = scanAmountText;

/** Convenience for pipelines that only need a value when it is unambiguous. */
export function parsePriceValue(
  text: string,
  rules?: PricingRules | NumberFormatRule,
): number | null {
  const parsed = parsePriceText(text, rules);
  if (parsed.ambiguous || parsed.invalid.length || parsed.amounts.length !== 1) return null;
  return parsed.amounts[0].value;
}
