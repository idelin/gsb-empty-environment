// Rule declarations and their per-value transforms.
//
// Every rule explicitly declares the paths it reads and writes; for
// value-transform rules both default to `targets`. Rule order is the
// array order of the rule set: fully explicit, no implicit priority.

import type { JsonValue, PathSegment } from "./json.ts";

export type PatternSegment = PathSegment | "*";
export type PathPattern = readonly PatternSegment[];

export function matchPattern(pattern: PathPattern, path: readonly PathSegment[]): boolean {
  if (pattern.length !== path.length) return false;
  return pattern.every((seg, i) => seg === "*" || seg === path[i]);
}

interface RuleCommon {
  readonly id: string;
  readonly targets: PathPattern[];
  /** Defaults to `targets` for value-transform rules. */
  readonly reads?: PathPattern[];
  /** Defaults to `targets` for value-transform rules. */
  readonly writes?: PathPattern[];
}

/** Trims / collapses whitespace of string values. Never touches null:
 *  null and whitespace-only strings are NOT treated as equivalent. */
export interface WhitespaceRule extends RuleCommon {
  readonly kind: "whitespace";
  readonly mode: "trim" | "collapse";
}

/** Parses date strings using an explicit list of declared formats
 *  (tokens YYYY, MM, DD). Unparseable strings are errors; the original
 *  value is preserved. No year/month/day order is ever guessed. */
export interface DateRule extends RuleCommon {
  readonly kind: "date";
  readonly formats: string[];
}

/** Maps declared alias strings to canonical values. */
export interface EnumAliasRule extends RuleCommon {
  readonly kind: "enum-alias";
  readonly aliases: Record<string, string>;
}

/** Copies a value from one concrete path to another. Exists so that
 *  read/write dependency graphs can express cross-field triggers. */
export interface CopyRule extends RuleCommon {
  readonly kind: "copy";
  readonly from: readonly PathSegment[];
  readonly to: readonly PathSegment[];
}

export type Rule = WhitespaceRule | DateRule | EnumAliasRule | CopyRule;

export function ruleReads(rule: Rule): readonly PathPattern[] {
  if (rule.kind === "copy") return [rule.from];
  return rule.reads ?? rule.targets;
}

export function ruleWrites(rule: Rule): readonly PathPattern[] {
  if (rule.kind === "copy") return [rule.to];
  return rule.writes ?? rule.targets;
}

export type TransformResult =
  | { status: "changed"; value: JsonValue }
  | { status: "unchanged" }
  | { status: "error"; error: string };

export function normalizeWhitespace(value: string, mode: "trim" | "collapse"): string {
  const trimmed = value.trim();
  return mode === "trim" ? trimmed : trimmed.replace(/\s+/g, " ");
}

function whitespaceTransform(rule: WhitespaceRule, value: JsonValue): TransformResult {
  if (typeof value !== "string") return { status: "unchanged" };
  const next = normalizeWhitespace(value, rule.mode);
  return next === value ? { status: "unchanged" } : { status: "changed", value: next };
}

const DATE_TOKENS: Record<string, string> = {
  YYYY: "(\\d{4})",
  MM: "(\\d{1,2})",
  DD: "(\\d{1,2})",
};

interface CompiledFormat {
  re: RegExp;
  order: string[];
}

function compileFormat(format: string): CompiledFormat {
  const order: string[] = [];
  let pattern = "^";
  let i = 0;
  while (i < format.length) {
    const token = Object.keys(DATE_TOKENS).find((t) => format.startsWith(t, i));
    if (token) {
      order.push(token);
      pattern += DATE_TOKENS[token];
      i += token.length;
    } else {
      pattern += format[i].replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      i += 1;
    }
  }
  pattern += "$";
  return { re: new RegExp(pattern), order };
}

function isLeapYear(year: number): boolean {
  return (year % 4 === 0 && year % 100 !== 0) || year % 400 === 0;
}

function daysInMonth(year: number, month: number): number {
  const days = [31, isLeapYear(year) ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  return days[month - 1];
}

function pad(n: number, width: number): string {
  return String(n).padStart(width, "0");
}

function dateTransform(rule: DateRule, value: JsonValue): TransformResult {
  if (typeof value !== "string") return { status: "unchanged" };
  for (const format of rule.formats) {
    const compiled = compileFormat(format);
    const m = compiled.re.exec(value);
    if (!m) continue;
    const parts: Record<string, number> = {};
    compiled.order.forEach((token, idx) => {
      parts[token] = Number(m[idx + 1]);
    });
    const year = parts.YYYY;
    const month = parts.MM;
    const day = parts.DD;
    if (month < 1 || month > 12) {
      return { status: "error", error: `invalid month ${month} in "${value}" (format ${format})` };
    }
    if (day < 1 || day > daysInMonth(year, month)) {
      return { status: "error", error: `invalid day ${day} in "${value}" (format ${format})` };
    }
    const iso = `${pad(year, 4)}-${pad(month, 2)}-${pad(day, 2)}`;
    return iso === value ? { status: "unchanged" } : { status: "changed", value: iso };
  }
  return {
    status: "error",
    error: `unparseable date "${value}": no declared format matches (${rule.formats.join(", ")})`,
  };
}

function enumAliasTransform(rule: EnumAliasRule, value: JsonValue): TransformResult {
  if (typeof value !== "string") return { status: "unchanged" };
  if (!Object.prototype.hasOwnProperty.call(rule.aliases, value)) {
    return { status: "unchanged" };
  }
  const next = rule.aliases[value];
  return next === value ? { status: "unchanged" } : { status: "changed", value: next };
}

export function transformValue(rule: Rule, value: JsonValue): TransformResult {
  switch (rule.kind) {
    case "whitespace":
      return whitespaceTransform(rule, value);
    case "date":
      return dateTransform(rule, value);
    case "enum-alias":
      return enumAliasTransform(rule, value);
    case "copy":
      throw new Error("copy rules are handled by the engine, not transformValue");
  }
}
