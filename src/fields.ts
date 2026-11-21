import { LEVEL_RANK, normalizeLevel } from "./levels.ts";
import type {
  FieldRule,
  FieldRuleEntry,
  FieldRuleObject,
  NestedMode,
  SensitivityLevel,
  MaskingStrategy,
} from "./types.ts";

export interface ResolvedFieldRule {
  level: SensitivityLevel;
  strategy?: MaskingStrategy;
  nestedMode?: NestedMode;
  source: "explicit" | "inferred" | "default";
  pattern?: string;
}

interface CompiledRule {
  pattern: string;
  rawPattern: string | RegExp;
  rule: FieldRuleObject;
  matcher: (fieldName: string, path: readonly string[]) => boolean;
}

const INFERRED_PATTERNS: ReadonlyArray<[string, SensitivityLevel]> = [
  ["password", "restricted"],
  ["passwd", "restricted"],
  ["pwd", "restricted"],
  ["secret", "restricted"],
  ["privatekey", "restricted"],
  ["apikey", "restricted"],
  ["accesstoken", "restricted"],
  ["refreshtoken", "restricted"],
  ["authorization", "restricted"],
  ["cookie", "restricted"],
  ["sessionid", "restricted"],
  ["cvv", "restricted"],
  ["cvc", "restricted"],
  ["pin", "restricted"],
  ["phone", "sensitive"],
  ["mobile", "sensitive"],
  ["telephone", "sensitive"],
  ["email", "sensitive"],
  ["idcard", "sensitive"],
  ["idnumber", "sensitive"],
  ["bankcard", "sensitive"],
  ["cardnumber", "sensitive"],
  ["creditcard", "sensitive"],
  ["ssn", "sensitive"],
  ["passport", "sensitive"],
  ["address", "sensitive"],
  ["birthday", "sensitive"],
  ["dateofbirth", "sensitive"],
  ["手机号", "sensitive"],
  ["手机", "sensitive"],
  ["电话号码", "sensitive"],
  ["电话", "sensitive"],
  ["邮箱", "sensitive"],
  ["电子邮箱", "sensitive"],
  ["身份证", "sensitive"],
  ["身份证号", "sensitive"],
  ["密码", "restricted"],
  ["银行卡", "sensitive"],
  ["卡号", "sensitive"],
  ["地址", "sensitive"],
  ["生日", "sensitive"],
  ["姓名", "sensitive"],
];

const INFERRED_EXACT = new Map(INFERRED_PATTERNS);
const INFERRED_CJK = INFERRED_PATTERNS.filter(([needle]) =>
  /[\u3400-\u9FFF]/.test(needle),
);

export function normalizeSegment(value: string): string {
  return value
    .normalize("NFKC")
    .toLowerCase()
    .replace(/[\s_.-]+/g, "");
}

function normalizePath(path: readonly string[]): string[] {
  return path.map((segment) => normalizeSegment(segment));
}

function toRuleObject(rule: FieldRule): FieldRuleObject {
  if (typeof rule === "object" && rule !== null) {
    return {
      level: normalizeLevel(rule.level),
      strategy: rule.strategy,
      nestedMode: rule.nestedMode,
    };
  }
  return { level: normalizeLevel(rule) };
}

function matchSegments(
  pattern: readonly string[],
  actual: readonly string[],
  patternIndex = 0,
  actualIndex = 0,
): boolean {
  if (patternIndex === pattern.length) {
    return actualIndex === actual.length;
  }

  const part = pattern[patternIndex];
  if (part === "**") {
    for (let skip = actualIndex; skip <= actual.length; skip += 1) {
      if (matchSegments(pattern, actual, patternIndex + 1, skip)) {
        return true;
      }
    }
    return false;
  }

  if (actualIndex >= actual.length) {
    return false;
  }

  if (part === "*" || part === "?") {
    return matchSegments(
      pattern,
      actual,
      patternIndex + 1,
      actualIndex + 1,
    );
  }

  if (!part.includes("*") && !part.includes("?")) {
    if (part !== actual[actualIndex]) {
      return false;
    }
    return matchSegments(
      pattern,
      actual,
      patternIndex + 1,
      actualIndex + 1,
    );
  }

  const expression = new RegExp(
    `^${part
      .split("")
      .map((character) => {
        if (character === "*") return "[^.]*";
        if (character === "?") return ".";
        return character.replace(/[\\^$+{}()[\]|]/g, "\\$&");
      })
      .join("")}$`,
  );
  if (!expression.test(actual[actualIndex])) {
    return false;
  }
  return matchSegments(pattern, actual, patternIndex + 1, actualIndex + 1);
}

function parsePatternPath(pattern: string): string[] {
  return pattern
    .split(".")
    .map((part) => part.replace(/\[[^\]]*\]/g, ""))
    .filter((part) => part.length > 0)
    .map((part) => (part === "**" || part === "*" ? part : normalizeSegment(part)));
}

function compileStringMatcher(
  rawPattern: string,
): (fieldName: string, path: readonly string[]) => boolean {
  const pattern = rawPattern.trim().replace(/^\$?\.?/, "");
  const hasPathSyntax = pattern.includes(".") || /[*?[\]]/.test(pattern);

  if (!hasPathSyntax) {
    const normalized = normalizeSegment(pattern);
    return (fieldName) => normalizeSegment(fieldName) === normalized;
  }

  const parts = parsePatternPath(pattern);
  return (fieldName, path) => {
    if (matchSegments(parts, normalizePath(path))) {
      return true;
    }
    return matchSegments(parts, [normalizeSegment(fieldName)]);
  };
}

function compileRule(
  rawPattern: string | RegExp,
  rule: FieldRule,
): CompiledRule {
  if (rawPattern instanceof RegExp) {
    const expression = new RegExp(
      rawPattern.source,
      rawPattern.flags.includes("g")
        ? rawPattern.flags
        : `${rawPattern.flags}g`,
    );
    return {
      pattern: rawPattern.toString(),
      rawPattern,
      rule: toRuleObject(rule),
      matcher: (fieldName, path) => {
        expression.lastIndex = 0;
        const byName = expression.test(fieldName);
        expression.lastIndex = 0;
        return byName || expression.test(path.join("."));
      },
    };
  }

  if (rawPattern.length > 1 && rawPattern.startsWith("/") && rawPattern.endsWith("/")) {
    const expression = new RegExp(rawPattern.slice(1, -1), "i");
    return {
      pattern: rawPattern,
      rawPattern,
      rule: toRuleObject(rule),
      matcher: (fieldName, path) => {
        expression.lastIndex = 0;
        const byName = expression.test(fieldName);
        expression.lastIndex = 0;
        return byName || expression.test(path.join("."));
      },
    };
  }

  return {
    pattern: rawPattern,
    rawPattern,
    rule: toRuleObject(rule),
    matcher: compileStringMatcher(rawPattern),
  };
}

function inferLevel(fieldName: string): SensitivityLevel | undefined {
  const normalized = normalizeSegment(fieldName);
  let level: SensitivityLevel | undefined;

  const combine = (candidate: SensitivityLevel): void => {
    if (!level || LEVEL_RANK[candidate] > LEVEL_RANK[level]) {
      level = candidate;
    }
  };

  const exact = INFERRED_EXACT.get(normalized);
  if (exact) combine(exact);

  for (const [needle, candidate] of INFERRED_CJK) {
    if (normalized.includes(needle)) combine(candidate);
  }

  const words: string[] = [];
  for (const part of fieldName.split(/[^A-Za-z0-9]+/)) {
    if (!part) continue;
    const split = part
      .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
      .replace(/([A-Z]+)([A-Z][a-z])/g, "$1 $2")
      .split(/\s+/);
    for (const word of split) {
      const normalizedWord = normalizeSegment(word);
      if (normalizedWord) words.push(normalizedWord);
    }
  }

  for (const word of words) {
    const candidate = INFERRED_EXACT.get(word);
    if (candidate) combine(candidate);
  }
  for (let index = 0; index < words.length - 1; index += 1) {
    const candidate = INFERRED_EXACT.get(
      `${words[index]}${words[index + 1]}`,
    );
    if (candidate) combine(candidate);
  }
  return level;
}

export class FieldResolver {
  private readonly rules: CompiledRule[];
  private readonly defaultLevel: SensitivityLevel;
  private readonly inferSensitiveFields: boolean;

  constructor(options: {
    fields?:
      | Record<string, FieldRule>
      | readonly FieldRuleEntry[]
      | ReadonlyMap<string, FieldRule>;
    defaultLevel?: SensitivityLevel;
    inferSensitiveFields?: boolean;
  }) {
    this.rules = [];
    const fields = options.fields;
    if (Array.isArray(fields)) {
      for (const entry of fields) {
        this.rules.push(compileRule(entry.pattern, entry.rule));
      }
    } else if (fields instanceof Map) {
      for (const [pattern, rule] of fields.entries()) {
        this.rules.push(compileRule(pattern, rule));
      }
    } else if (fields) {
      for (const [pattern, rule] of Object.entries(fields)) {
        this.rules.push(compileRule(pattern, rule));
      }
    }
    this.defaultLevel = options.defaultLevel ?? "public";
    this.inferSensitiveFields = options.inferSensitiveFields ?? true;
  }

  resolve(
    fieldName: string,
    path: readonly string[],
    valueType?: string,
  ): ResolvedFieldRule {
    const matches = this.rules.filter((rule) =>
      rule.matcher(fieldName, path),
    );

    if (matches.length > 0) {
      let selected = matches[0];
      for (const match of matches) {
        if (
          LEVEL_RANK[match.rule.level] > LEVEL_RANK[selected.rule.level]
        ) {
          selected = match;
        }
      }
      return {
        level: selected.rule.level,
        strategy: selected.rule.strategy,
        nestedMode: selected.rule.nestedMode,
        source: "explicit",
        pattern: selected.pattern,
      };
    }

    if (this.inferSensitiveFields && valueType !== "boolean") {
      const inferred = inferLevel(fieldName);
      if (inferred) {
        return { level: inferred, source: "inferred" };
      }
    }

    return { level: this.defaultLevel, source: "default" };
  }
}
