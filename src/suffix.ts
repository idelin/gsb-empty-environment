import { digestValue } from "./canonical.ts";
import type {
  ScriptPolicy,
  SuffixRule,
  SuffixRuleKind,
  SuffixTable,
} from "./types.ts";
import {
  normalizeLabelKey,
  normalizeSuffix,
  splitDomainText,
} from "./unicode.ts";

export interface ParsedSuffixRule {
  readonly order: number;
  readonly rule: SuffixRule;
  readonly kind: SuffixRuleKind;
  readonly labels: readonly string[];
}

export interface SuffixResolution {
  readonly version: string;
  readonly suffix: string;
  readonly suffixLabels: readonly string[];
  readonly registrableLabels: readonly string[] | null;
  readonly rule: SuffixRule | null;
  readonly kind: SuffixRuleKind | "default";
  readonly policy: ScriptPolicy | undefined;
}

const parsedRuleCache = new WeakMap<SuffixTable, readonly ParsedSuffixRule[]>();

function parsedRules(table: SuffixTable): readonly ParsedSuffixRule[] {
  const cached = parsedRuleCache.get(table);
  if (cached) return cached;
  const parsed = table.rules.map((rule, index) => parseSuffixRule(rule, index));
  parsedRuleCache.set(table, parsed);
  return parsed;
}

export function parseSuffixRule(
  rule: SuffixRule,
  order: number,
): ParsedSuffixRule {
  if (!rule || typeof rule.suffix !== "string") {
    throw new TypeError(`suffix rule ${order}: missing suffix`);
  }
  let text = rule.suffix.trim();
  let inferred: SuffixRuleKind = "normal";
  if (text.startsWith("!")) {
    inferred = "exception";
    text = text.slice(1);
  } else if (text.startsWith("*.")) {
    inferred = "wildcard";
    text = text.slice(2);
  }
  const kind = rule.kind ?? inferred;
  if (kind !== inferred && inferred !== "normal") {
    throw new TypeError(
      `suffix rule ${order}: marker in '${rule.suffix}' disagrees with kind '${kind}'`,
    );
  }
  if (
    kind !== "normal" &&
    kind !== "wildcard" &&
    kind !== "exception"
  ) {
    throw new TypeError(`suffix rule ${order}: unknown kind '${kind}'`);
  }
  if (kind === "wildcard" && text.startsWith("*.")) text = text.slice(2);
  const labels = splitDomainText(text).map(normalizeLabelKey);
  if (labels.length === 0 || labels.some((label) => label.length === 0)) {
    throw new TypeError(`suffix rule ${order}: empty suffix`);
  }
  if (kind === "exception" && labels.length < 2) {
    throw new TypeError(
      `suffix rule ${order}: an exception needs at least two labels`,
    );
  }
  return { order, rule, kind, labels };
}

export function assertSuffixTable(table: SuffixTable): void {
  if (!table || typeof table.version !== "string" || table.version.length === 0) {
    throw new TypeError("suffix table needs a non-empty version");
  }
  if (!Array.isArray(table.rules)) {
    throw new TypeError("suffix table rules must be an array");
  }
  parsedRules(table);
}

function endsWithLabels(
  labels: readonly string[],
  tail: readonly string[],
): boolean {
  if (tail.length > labels.length) return false;
  const offset = labels.length - tail.length;
  for (let i = 0; i < tail.length; i++) {
    if (labels[offset + i] !== tail[i]) return false;
  }
  return true;
}

/**
 * Resolve the public suffix with the PSL precedence rules: an exception wins
 * over wildcard and normal rules; otherwise the longest matching rule wins.
 */
export function effectiveSuffix(
  labels: readonly string[],
  table: SuffixTable,
  fallbackPolicy?: ScriptPolicy,
): SuffixResolution {
  assertSuffixTable(table);
  const normalized = labels.map(normalizeLabelKey);
  const parsed = parsedRules(table);

  let exception: ParsedSuffixRule | null = null;
  for (const candidate of parsed) {
    if (candidate.kind !== "exception") continue;
    if (!endsWithLabels(normalized, candidate.labels)) continue;
    if (
      !exception ||
      candidate.labels.length > exception.labels.length
    ) {
      exception = candidate;
    }
  }

  let publicCount = 0;
  let selected: ParsedSuffixRule | null = null;
  let kind: SuffixRuleKind | "default" = "default";

  if (exception) {
    selected = exception;
    kind = "exception";
    publicCount = exception.labels.length - 1;
  } else {
    for (const candidate of parsed) {
      if (candidate.kind === "exception") continue;
      if (!endsWithLabels(normalized, candidate.labels)) continue;
      let candidateCount: number;
      if (candidate.kind === "wildcard") {
        if (normalized.length < candidate.labels.length + 1) continue;
        candidateCount = candidate.labels.length + 1;
      } else {
        candidateCount = candidate.labels.length;
      }
      const moreSpecificNormal =
        selected !== null &&
        candidateCount === publicCount &&
        kind === "wildcard" &&
        candidate.kind === "normal";
      if (!selected || candidateCount > publicCount || moreSpecificNormal) {
        selected = candidate;
        kind = candidate.kind;
        publicCount = candidateCount;
      }
    }
  }

  if (!selected) {
    publicCount = normalized.length > 0 ? 1 : 0;
  }

  const suffixLabels =
    publicCount > 0 ? normalized.slice(normalized.length - publicCount) : [];
  const registrableLabels =
    normalized.length > suffixLabels.length
      ? normalized.slice(-(suffixLabels.length + 1))
      : null;

  return {
    version: table.version,
    suffix: suffixLabels.join("."),
    suffixLabels,
    registrableLabels,
    rule: selected?.rule ?? null,
    kind,
    policy: selected?.rule.policy ?? table.defaultPolicy ?? fallbackPolicy,
  };
}

export function suffixApplies(
  aliasSuffix: string,
  currentSuffix: string,
): boolean {
  const scope = normalizeSuffix(aliasSuffix);
  if (scope === "*") return true;
  return scope === normalizeSuffix(currentSuffix);
}

export function suffixTableDigest(table: SuffixTable): string {
  return digestValue({
    kind: "suffix-table",
    version: table.version,
    rules: table.rules.map((rule) => ({
      suffix: rule.suffix,
      kind: rule.kind ?? null,
      policy: rule.policy ?? null,
    })),
    defaultPolicy: table.defaultPolicy ?? null,
  });
}
