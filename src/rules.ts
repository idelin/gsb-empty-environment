import type { DateRule, EnumRule, WhitespaceRule } from "./types.ts";

export type RuleResult =
  | { ok: true; value: string }
  | { ok: false; code: string; message: string };

/* ------------------------------- 空白 ------------------------------- */

export function applyWhitespace(rule: WhitespaceRule, input: string): RuleResult {
  if (rule.mode === "trim") return { ok: true, value: input.trim() };
  return { ok: true, value: input.trim().replace(/\s+/g, " ") };
}

/** 空白规则声明的有限域：若干有代表性的空白形态。 */
export const WHITESPACE_PROBES: readonly string[] = Object.freeze([
  "x",
  " x",
  "x ",
  " x ",
  "  x  ",
  "a b",
  "a  b",
  " a  b ",
  "\ta\nb\t",
  "",
  " ",
  "   ",
]);

/* ------------------------------- 日期 ------------------------------- */

interface DateParts {
  year: number;
  month: number; // 1-12
  day: number; // 1-31
  hour: number;
  minute: number;
  second: number;
}

const TOKEN_PATTERNS: Record<string, { re: string; group: keyof DateParts | null }> = {
  YYYY: { re: "(\\d{4})", group: "year" },
  MM: { re: "(\\d{2})", group: "month" },
  DD: { re: "(\\d{2})", group: "day" },
  HH: { re: "(\\d{2})", group: "hour" },
  mm: { re: "(\\d{2})", group: "minute" },
  ss: { re: "(\\d{2})", group: "second" },
};

/** 把格式串编译为正则与字段抽取顺序。分隔符按字面匹配。 */
function compileFormat(format: string): { re: RegExp; order: (keyof DateParts)[] } {
  const order: (keyof DateParts)[] = [];
  let source = "^";
  let i = 0;
  const keys = Object.keys(TOKEN_PATTERNS);
  while (i < format.length) {
    const key = keys.find((k) => format.startsWith(k, i));
    if (key) {
      const t = TOKEN_PATTERNS[key];
      source += t.re;
      if (t.group) order.push(t.group);
      i += key.length;
    } else {
      source += format[i].replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      i += 1;
    }
  }
  source += "$";
  return { re: new RegExp(source), order };
}

const DAYS_IN_MONTH = [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];

function isLeap(year: number): boolean {
  return (year % 4 === 0 && year % 100 !== 0) || year % 400 === 0;
}

function validParts(p: DateParts): boolean {
  if (p.year < 1 || p.year > 9999) return false;
  if (p.month < 1 || p.month > 12) return false;
  const maxDay = p.month === 2 && isLeap(p.year) ? 29 : DAYS_IN_MONTH[p.month - 1];
  if (p.day < 1 || p.day > maxDay) return false;
  if (p.hour > 23 || p.minute > 59 || p.second > 59) return false;
  return true;
}

function pad(n: number): string {
  return String(n).padStart(2, "0");
}

/** 按格式渲染日期。 */
export function formatDate(parts: DateParts, format: string): string {
  const values: Record<string, string> = {
    YYYY: String(parts.year).padStart(4, "0"),
    MM: pad(parts.month),
    DD: pad(parts.day),
    HH: pad(parts.hour),
    mm: pad(parts.minute),
    ss: pad(parts.second),
  };
  let out = "";
  let i = 0;
  const keys = Object.keys(TOKEN_PATTERNS);
  while (i < format.length) {
    const key = keys.find((k) => format.startsWith(k, i));
    if (key) {
      out += values[key];
      i += key.length;
    } else {
      out += format[i];
      i += 1;
    }
  }
  return out;
}

function parseWithFormat(input: string, format: string): DateParts | null {
  const { re, order } = compileFormat(format);
  const m = re.exec(input);
  if (!m) return null;
  const p: DateParts = { year: 1, month: 1, day: 1, hour: 0, minute: 0, second: 0 };
  order.forEach((field, idx) => {
    p[field] = Number(m[idx + 1]);
  });
  return validParts(p) ? p : null;
}

function sameParts(a: DateParts, b: DateParts): boolean {
  return (
    a.year === b.year && a.month === b.month && a.day === b.day &&
    a.hour === b.hour && a.minute === b.minute && a.second === b.second
  );
}

/**
 * 严格解析：
 * - 没有任何格式匹配 -> DATE_UNPARSEABLE（绝不猜测年月日顺序）
 * - 多个格式匹配但得到不同日期 -> DATE_AMBIGUOUS
 * - 否则用 outputFormat 输出
 */
export function applyDate(rule: DateRule, input: string): RuleResult {
  let parsed: DateParts | null = null;
  for (const fmt of rule.formats) {
    const candidate = parseWithFormat(input, fmt);
    if (!candidate) continue;
    if (parsed && !sameParts(parsed, candidate)) {
      return {
        ok: false,
        code: "DATE_AMBIGUOUS",
        message: `输入 "${input}" 在多个声明格式下解析为不同日期，拒绝猜测`,
      };
    }
    parsed = candidate;
  }
  if (!parsed) {
    return {
      ok: false,
      code: "DATE_UNPARSEABLE",
      message: `输入 "${input}" 不匹配任何声明日期格式: ${rule.formats.join(" | ")}`,
    };
  }
  return { ok: true, value: formatDate(parsed, rule.outputFormat) };
}

/**
 * 日期规则声明的有限输入域（用于幂等反例搜索）。
 * 对每个探测年、每月 1/15/末日 生成每种输入格式的合法样例，
 * 再加上边界日期与一批典型不可解析串。
 */
export function dateDomain(rule: DateRule): string[] {
  const years = rule.probeYears && rule.probeYears.length > 0 ? rule.probeYears : [2020, 2021, 2024];
  const probes: Array<[number, number, number]> = [];
  for (const year of years) {
    for (let month = 1; month <= 12; month++) {
      const lastDay = month === 2 && isLeap(year) ? 29 : DAYS_IN_MONTH[month - 1];
      for (const day of [1, 15, lastDay]) probes.push([year, month, day]);
    }
  }
  const extras: Array<[number, number, number]> = [
    [2000, 2, 29],
    [1999, 2, 28],
    [2023, 12, 31],
    [2024, 1, 1],
  ];
  const domain = new Set<string>();
  const emit = (y: number, m: number, d: number): void => {
    for (const fmt of rule.formats) {
      domain.add(formatDate({ year: y, month: m, day: d, hour: 0, minute: 0, second: 0 }, fmt));
    }
  };
  for (const [y, m, d] of probes) emit(y, m, d);
  for (const [y, m, d] of extras) emit(y, m, d);
  // 输出格式自身产物是幂等性的关键候选。
  for (const [y, m, d] of [...probes, ...extras]) {
    domain.add(formatDate({ year: y, month: m, day: d, hour: 0, minute: 0, second: 0 }, rule.outputFormat));
  }
  for (const bad of ["2021-13-01", "2021-02-30", "not-a-date", "02/30/2021", "", "2021/02/29", "21-02-03"]) {
    domain.add(bad);
  }
  return [...domain];
}

/** 日期规则输出形态的结构判断：是否匹配某个格式的字面形态。 */
export function matchesFormatShape(value: string, format: string): boolean {
  return compileFormat(format).re.test(value);
}

/* ------------------------------- 枚举 ------------------------------- */

export function applyEnum(rule: EnumRule, input: string): RuleResult {
  if (Object.prototype.hasOwnProperty.call(rule.map, input)) {
    return { ok: true, value: rule.map[input] };
  }
  if ((rule.onUnknown ?? "error") === "keep") {
    return { ok: true, value: input };
  }
  return {
    ok: false,
    code: "ENUM_UNKNOWN",
    message: `值 "${input}" 不在枚举别名表中: ${Object.keys(rule.map).join(", ")}`,
  };
}

/** 枚举规则声明的有限输入域：所有别名 + 所有规范值 + 一个未知值。 */
export function enumDomain(rule: EnumRule): string[] {
  const aliases = Object.keys(rule.map);
  const canonicals = new Set(Object.values(rule.map));
  return [...new Set([...aliases, ...canonicals, "__unknown__"])];
}
