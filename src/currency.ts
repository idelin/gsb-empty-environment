import type {
  CurrencyInput,
  CurrencyMention,
  CurrencyRule,
  PricingRules,
} from './types.ts';

interface CurrencyDefinition {
  code: string;
  symbols: string[];
  names?: string[];
}

export const CURRENCY_DEFINITIONS: CurrencyDefinition[] = [
  { code: 'USD', symbols: ['US$', 'USD$', '$'], names: ['美元', 'US dollar'] },
  { code: 'EUR', symbols: ['€'], names: ['欧元', 'euro'] },
  { code: 'GBP', symbols: ['£', 'UK£'], names: ['英镑'] },
  { code: 'JPY', symbols: ['¥', '￥', 'JP¥'], names: ['日元', '円'] },
  { code: 'CNY', symbols: ['CN¥', 'RMB¥', '¥', '￥'], names: ['人民币', '元'] },
  { code: 'HKD', symbols: ['HK$', '$'], names: ['港元', '港币'] },
  { code: 'CAD', symbols: ['CA$', 'C$', '$'], names: ['加元'] },
  { code: 'AUD', symbols: ['A$', 'AU$', '$'], names: ['澳元'] },
  { code: 'NZD', symbols: ['NZ$', '$'], names: ['新西兰元'] },
  { code: 'SGD', symbols: ['S$', 'SG$', '$'], names: ['新加坡元'] },
  { code: 'CHF', symbols: ['CHF', 'Fr.'], names: ['瑞士法郎'] },
  { code: 'INR', symbols: ['₹', 'Rs', 'Rs.'], names: ['印度卢比'] },
  { code: 'BRL', symbols: ['R$'], names: ['巴西雷亚尔'] },
  { code: 'RUB', symbols: ['₽', 'руб.'], names: ['卢布'] },
  { code: 'KRW', symbols: ['₩', 'KRW'], names: ['韩元'] },
  { code: 'TRY', symbols: ['₺'], names: ['土耳其里拉'] },
  { code: 'SEK', symbols: ['kr'], names: ['瑞典克朗'] },
  { code: 'NOK', symbols: ['kr'], names: ['挪威克朗'] },
  { code: 'DKK', symbols: ['kr'], names: ['丹麦克朗'] },
  { code: 'PLN', symbols: ['zł'], names: ['波兰兹罗提'] },
  { code: 'CZK', symbols: ['Kč'], names: ['捷克克朗'] },
  { code: 'HUF', symbols: ['Ft'], names: ['匈牙利福林'] },
  { code: 'ZAR', symbols: ['ZAR'], names: ['南非兰特'] },
  { code: 'MXN', symbols: ['MX$', '$'], names: ['墨西哥比索'] },
  { code: 'TWD', symbols: ['NT$', '$'], names: ['新台币'] },
  { code: 'THB', symbols: ['฿'], names: ['泰铢'] },
  { code: 'VND', symbols: ['₫'], names: ['越南盾'] },
  { code: 'IDR', symbols: ['Rp'], names: ['印尼盾'] },
  { code: 'MYR', symbols: ['RM'], names: ['马来西亚林吉特'] },
  { code: 'PHP', symbols: ['₱'], names: ['菲律宾比索'] },
  { code: 'AED', symbols: ['د.إ', 'AED'], names: ['迪拉姆'] },
  { code: 'SAR', symbols: ['ر.س', 'SAR'], names: ['沙特里亚尔'] },
  { code: 'ILS', symbols: ['₪', 'ILS'], names: ['以色列谢克尔'] },
  { code: 'UAH', symbols: ['₴'], names: ['乌克兰格里夫纳'] },
  { code: 'NGN', symbols: ['₦'], names: ['奈拉'] },
  { code: 'KES', symbols: ['KSh'], names: ['肯尼亚先令'] },
  { code: 'EGP', symbols: ['E£'], names: ['埃及镑'] },
  { code: 'CLP', symbols: ['CLP$', '$'], names: ['智利比索'] },
  { code: 'COP', symbols: ['COL$'], names: ['哥伦比亚比索'] },
  { code: 'ARS', symbols: ['AR$', '$'], names: ['阿根廷比索'] },
  { code: 'PEN', symbols: ['S/'], names: ['秘鲁索尔'] },
  { code: 'TND', symbols: ['د.ت'], names: ['突尼斯第纳尔'] },
];

const CODE_BY_DEFINITION = new Map(CURRENCY_DEFINITIONS.map((definition) => [definition.code, definition]));

function symbolMap(): Map<string, string[]> {
  const map = new Map<string, string[]>();
  for (const definition of CURRENCY_DEFINITIONS) {
    for (const symbol of definition.symbols) {
      if (symbol.length === 3 && /^[A-Z]+$/.test(symbol)) continue;
      const codes = map.get(symbol) ?? [];
      if (!codes.includes(definition.code)) codes.push(definition.code);
      map.set(symbol, codes);
    }
  }
  return map;
}

const SYMBOL_CODES = symbolMap();

function nameMap(): Map<string, string[]> {
  const map = new Map<string, string[]>();
  for (const definition of CURRENCY_DEFINITIONS) {
    for (const name of definition.names ?? []) {
      const codes = map.get(name.toLowerCase()) ?? [];
      if (!codes.includes(definition.code)) codes.push(definition.code);
      map.set(name.toLowerCase(), codes);
    }
  }
  return map;
}

const NAME_CODES = nameMap();

const DIRECT_CODE_PREFIXES = new Set([
  'USD', 'EUR', 'GBP', 'JPY', 'CNY', 'HKD', 'CAD', 'AUD', 'NZD', 'SGD',
  'CHF', 'INR', 'BRL', 'RUB', 'KRW', 'TRY', 'SEK', 'NOK', 'DKK', 'PLN',
  'CZK', 'HUF', 'ZAR', 'MXN', 'TWD', 'THB', 'VND', 'IDR', 'MYR', 'PHP',
  'AED', 'SAR', 'ILS', 'UAH', 'NGN', 'KES', 'EGP', 'CLP', 'COP', 'ARS',
  'PEN', 'TND',
]);

const CODE_ALIASES: Record<string, string> = {
  RMB: 'CNY',
  CNH: 'CNY',
  NTD: 'TWD',
};

export interface ExpectedCurrency {
  configured: boolean;
  codes: string[];
  symbols: string[];
  primary?: string;
  ambiguous: boolean;
}

function addCurrencyToken(token: string, result: { codes: string[]; symbols: string[] }): void {
  const upper = token.toUpperCase();
  const alias = CODE_ALIASES[upper];
  if (alias) {
    if (!result.codes.includes(alias)) result.codes.push(alias);
    return;
  }
  if (DIRECT_CODE_PREFIXES.has(upper)) {
    if (!result.codes.includes(upper)) result.codes.push(upper);
    return;
  }
  const symbolCodes = SYMBOL_CODES.get(token) ?? SYMBOL_CODES.get(token.toLowerCase());
  if (symbolCodes) {
    for (const code of symbolCodes) if (!result.codes.includes(code)) result.codes.push(code);
    return;
  }
  const nameCodes = NAME_CODES.get(token.toLowerCase());
  if (nameCodes) {
    for (const code of nameCodes) if (!result.codes.includes(code)) result.codes.push(code);
  }
}

function fromRule(rule: CurrencyInput, result: { codes: string[]; symbols: string[] }): void {
  if (typeof rule === 'string') {
    addCurrencyToken(rule, result);
    return;
  }
  if (Array.isArray(rule)) {
    for (const entry of rule) fromRule(entry, result);
    return;
  }
  const object = rule as CurrencyRule;
  for (const token of [object.code, object.currency, object.expected]) {
    if (token) addCurrencyToken(token, result);
  }
  for (const token of object.codes ?? []) addCurrencyToken(token, result);
  const hasExplicitCode = result.codes.length > 0;
  for (const token of object.symbols ?? []) {
    if (!result.symbols.includes(token)) result.symbols.push(token);
    if (!hasExplicitCode) addCurrencyToken(token, result);
  }
  if (object.symbol) {
    if (!result.symbols.includes(object.symbol)) result.symbols.push(object.symbol);
    if (!hasExplicitCode) addCurrencyToken(object.symbol, result);
  }
}

export function expectedCurrency(rules: PricingRules): ExpectedCurrency {
  const result: { codes: string[]; symbols: string[] } = { codes: [], symbols: [] };
  if (rules.currency !== undefined) fromRule(rules.currency, result);
  for (const token of [rules.currencyCode, rules.expectedCurrency]) {
    if (token) addCurrencyToken(token, result);
  }
  if (rules.currencySymbol) {
    if (!result.symbols.includes(rules.currencySymbol)) result.symbols.push(rules.currencySymbol);
    if (!result.codes.length) addCurrencyToken(rules.currencySymbol, result);
  }
  for (const token of rules.currencies ?? []) addCurrencyToken(token, result);
  return {
    configured: result.codes.length > 0 || result.symbols.length > 0,
    codes: result.codes,
    symbols: result.symbols,
    primary: result.codes.length === 1 ? result.codes[0] : undefined,
    ambiguous: result.codes.length > 1,
  };
}

function isAsciiLetter(char: string): boolean {
  return /[A-Za-z]/.test(char);
}

interface RawMention {
  start: number;
  end: number;
  text: string;
  codes: string[];
  kind: 'symbol' | 'code';
}

function overlaps(a: RawMention, b: RawMention): boolean {
  return a.start < b.end && b.start < a.end;
}

export function findCurrencyMentions(text: string): CurrencyMention[] {
  const candidates: RawMention[] = [];

  for (const code of CODE_BY_DEFINITION.keys()) {
    const needle = code.toLowerCase();
    let from = 0;
    while (from < text.length) {
      const index = text.toLowerCase().indexOf(needle, from);
      if (index < 0) break;
      const before = index > 0 ? text[index - 1] : '';
      const after = index + code.length < text.length ? text[index + code.length] : '';
      if ((!before || !isAsciiLetter(before)) && (!after || !isAsciiLetter(after))) {
        candidates.push({
          start: index,
          end: index + code.length,
          text: text.slice(index, index + code.length),
          codes: [code],
          kind: 'code',
        });
      }
      from = index + 1;
    }
  }

  for (const [alias, code] of Object.entries(CODE_ALIASES)) {
    let from = 0;
    while (from < text.length) {
      const index = text.toLowerCase().indexOf(alias.toLowerCase(), from);
      if (index < 0) break;
      const before = index > 0 ? text[index - 1] : '';
      const after = index + alias.length < text.length ? text[index + alias.length] : '';
      if ((!before || !isAsciiLetter(before)) && (!after || !isAsciiLetter(after))) {
        candidates.push({
          start: index,
          end: index + alias.length,
          text: text.slice(index, index + alias.length),
          codes: [code],
          kind: 'code',
        });
      }
      from = index + 1;
    }
  }

  for (const [symbol, codes] of SYMBOL_CODES) {
    let from = 0;
    while (from <= text.length - symbol.length) {
      const index = text.indexOf(symbol, from);
      if (index < 0) break;
      const before = index > 0 ? text[index - 1] : '';
      const after = index + symbol.length < text.length ? text[index + symbol.length] : '';
      if (/^[A-Za-z]+$/.test(symbol) && (isAsciiLetter(before) || isAsciiLetter(after))) {
        from = index + 1;
        continue;
      }
      candidates.push({
        start: index,
        end: index + symbol.length,
        text: symbol,
        codes,
        kind: 'symbol',
      });
      from = index + 1;
    }
  }

  for (const [name, codes] of NAME_CODES) {
    let from = 0;
    while (from <= text.length - name.length) {
      const index = text.toLowerCase().indexOf(name, from);
      if (index < 0) break;
      candidates.push({
        start: index,
        end: index + name.length,
        text: text.slice(index, index + name.length),
        codes,
        kind: 'symbol',
      });
      from = index + 1;
    }
  }

  candidates.sort((a, b) => a.start - b.start || b.end - b.start - (a.end - a.start));
  const accepted: RawMention[] = [];
  for (const candidate of candidates) {
    if (accepted.some((entry) => overlaps(entry, candidate))) continue;
    accepted.push(candidate);
  }
  return accepted.sort((a, b) => a.start - b.start);
}

export function currencyCodesForToken(token: string): string[] {
  const upper = token.toUpperCase();
  if (CODE_ALIASES[upper]) return [CODE_ALIASES[upper]];
  if (DIRECT_CODE_PREFIXES.has(upper)) return [upper];
  return SYMBOL_CODES.get(token) ?? SYMBOL_CODES.get(token.toLowerCase()) ?? [];
}

export function canonicalCurrency(code: string): string {
  return code.trim().toUpperCase();
}

/** Values such as "$" may be explicitly configured, but only a code can
 * disambiguate them without looking at other page evidence. */
export function isAmbiguousCurrencySymbol(symbol: string): boolean {
  return (SYMBOL_CODES.get(symbol)?.length ?? 0) > 1;
}

export function mergeCurrencyCodes(codes: string[]): string[] {
  return Array.from(new Set(codes.map(canonicalCurrency).filter(Boolean)));
}
