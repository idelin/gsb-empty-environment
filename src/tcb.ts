import { asNumber, isPlainObject } from "./canonical.ts";
import type {
  NormalizedTcb,
  TcbCompareOptions,
  TcbComparison,
  TcbDocument,
  TcbRule,
  TcbValue,
  TcbVector,
} from "./types.ts";

const META_KEYS = new Set([
  "epoch",
  "family",
  "namespace",
  "components",
  "versions",
  "version",
  "component",
  "name",
  "value",
  "id",
]);

function normalizeValue(value: unknown): TcbValue | undefined {
  if (
    typeof value === "number" ||
    typeof value === "string" ||
    typeof value === "boolean"
  ) {
    return value;
  }
  if (isPlainObject(value)) {
    return normalizeValue(value.version ?? value.value ?? value.svn);
  }
  return undefined;
}

/**
 * Accepts both common representations of a TCB version vector:
 *   { svn: 3, fmw: 7 }
 *   { components: { svn: 3, fmw: 7 } }
 *   [{ component: "svn", version: 3 }, ...]
 */
export function normalizeTcb(vector: TcbVector | undefined | null): NormalizedTcb {
  if (vector === undefined || vector === null) return { components: {} };
  const rawVector: unknown = vector;
  if (rawVector instanceof Map) {
    const components: Record<string, TcbValue> = {};
    for (const [key, value] of rawVector.entries()) {
      const name = asStringValue(key);
      const normalized = normalizeValue(value);
      if (name && normalized !== undefined) components[name] = normalized;
    }
    return { components };
  }
  if (typeof vector === "string") {
    if (vector.trim().startsWith("{") || vector.trim().startsWith("[")) {
      try {
        return normalizeTcb(JSON.parse(vector) as TcbVector);
      } catch {
        // Fall through to key=value parsing.
      }
    }
    const components: Record<string, TcbValue> = {};
    for (const match of vector.matchAll(
      /([A-Za-z][A-Za-z0-9_.-]*)\s*[:=]\s*([^,;\s]+)/g,
    )) {
      const value = normalizeValue(match[2]);
      if (value !== undefined) components[match[1]] = value;
    }
    return { components };
  }
  if (Array.isArray(vector)) {
    const components: Record<string, TcbValue> = {};
    for (const item of vector) {
      const rawItem: unknown = item;
      if (Array.isArray(rawItem) && rawItem.length >= 2) {
        const name = asStringValue(rawItem[0]);
        const value = normalizeValue(rawItem[1]);
        if (name && value !== undefined) components[name] = value;
        continue;
      }
      if (!isPlainObject(rawItem)) continue;
      const itemObject = rawItem;
      const name =
        typeof itemObject.component === "string"
          ? itemObject.component
          : typeof itemObject.name === "string"
            ? itemObject.name
            : typeof itemObject.id === "string"
              ? itemObject.id
              : undefined;
      const value = normalizeValue(
        itemObject.version ?? itemObject.value,
      );
      if (name && value !== undefined) components[name] = value;
    }
    return { components };
  }
  if (!isPlainObject(vector)) return { components: {} };
  const vectorObject = vector as TcbDocument;

  const meta: Pick<NormalizedTcb, "epoch" | "family" | "namespace"> = {};
  const epoch = asStringValue(vectorObject.epoch);
  const family = asStringValue(vectorObject.family);
  const namespace = asStringValue(vectorObject.namespace);
  if (epoch) meta.epoch = epoch;
  if (family) meta.family = family;
  if (namespace) meta.namespace = namespace;

  for (const key of ["components", "versions", "version"] as const) {
    if (Array.isArray(vectorObject[key])) {
      return {
        ...normalizeTcb(vectorObject[key] as TcbVector),
        ...meta,
      };
    }
  }

  const nested =
    isPlainObject(vectorObject.components)
      ? vectorObject.components
      : isPlainObject(vectorObject.versions)
        ? vectorObject.versions
        : isPlainObject(vectorObject.version)
          ? vectorObject.version
          : undefined;

  if (nested) {
    const components: Record<string, TcbValue> = {};
    for (const [key, value] of Object.entries(nested)) {
      const normalized = normalizeValue(value);
      if (normalized !== undefined) components[key] = normalized;
    }
    return { components, ...meta };
  }

  const components: Record<string, TcbValue> = {};
  for (const [key, value] of Object.entries(vectorObject)) {
    if (META_KEYS.has(key)) continue;
    const normalized = normalizeValue(value);
    if (normalized !== undefined) components[key] = normalized;
  }
  return { components, ...meta };
}

function asStringValue(value: unknown): string | undefined {
  return typeof value === "string" && value.length > 0
    ? value
    : typeof value === "number"
      ? String(value)
      : undefined;
}

function scalarComparison(
  candidate: TcbValue,
  baseline: TcbValue,
  rule: TcbRule,
): TcbComparison {
  if (rule === "ignore") return "equal";
  if (rule === "incomparable") return "incomparable";
  if (rule === "exact") {
    return candidate === baseline ? "equal" : "incomparable";
  }

  const candidateNumber = asNumber(candidate);
  const baselineNumber = asNumber(baseline);
  if (candidateNumber !== undefined && baselineNumber !== undefined) {
    if (candidateNumber > baselineNumber) return "newer";
    if (candidateNumber < baselineNumber) return "older";
    return "equal";
  }

  // Non numeric components can only be compared exactly.  A mismatch is not
  // "older": the two vectors live in different, incomparable revision spaces.
  if (candidate === baseline) return "equal";
  return "incomparable";
}

function defaultRule(value: TcbValue): TcbRule {
  return asNumber(value) !== undefined ? "monotonic" : "exact";
}

/**
 * Compare a candidate vector against a baseline.
 *
 * Crucially, a newer component never compensates for a missing or older
 * component.  Different component sets are incomparable by default, which also
 * makes independently versioned vendor namespaces incomparable.
 */
export function compareTcbVectors(
  candidateInput: TcbVector | undefined | null,
  baselineInput: TcbVector | undefined | null,
  options: TcbCompareOptions = {},
): TcbComparison {
  const candidate = normalizeTcb(candidateInput);
  const baseline = normalizeTcb(baselineInput);

  for (const namespaceKey of ["epoch", "family", "namespace"] as const) {
    const candidateNamespace = candidate[namespaceKey];
    const baselineNamespace = baseline[namespaceKey];
    if (
      candidateNamespace !== undefined &&
      baselineNamespace !== undefined &&
      candidateNamespace !== baselineNamespace
    ) {
      return "incomparable";
    }
  }

  const requireExactComponentSet = options.requireExactComponentSet ?? true;
  const allowExtraComponents = options.allowExtraComponents ?? false;
  const rules = options.rules ?? {};
  let sawNewer = false;
  let sawOlder = false;
  let sawIncomparable = false;

  if (Object.keys(baseline.components).length === 0) {
    return Object.keys(candidate.components).length > 0 ? "newer" : "equal";
  }

  for (const [name, baselineValue] of Object.entries(
    baseline.components,
  ).sort(([a], [b]) => a.localeCompare(b))) {
    const configured = rules[name] as unknown;
    const rule: TcbRule =
      typeof configured === "string"
        ? (configured.toLowerCase() as TcbRule)
        : isPlainObject(configured) && typeof configured.mode === "string"
          ? (configured.mode.toLowerCase() as TcbRule)
          : defaultRule(baselineValue);
    if (rule === "ignore") continue;
    if (!(name in candidate.components)) {
      sawIncomparable = true;
      continue;
    }
    const result = scalarComparison(candidate.components[name], baselineValue, rule);
    if (result === "incomparable") sawIncomparable = true;
    if (result === "older") sawOlder = true;
    if (result === "newer") sawNewer = true;
  }

  for (const [name, candidateValue] of Object.entries(
    candidate.components,
  ).sort(([a], [b]) => a.localeCompare(b))) {
    if (name in baseline.components) continue;
    const configured = rules[name] as unknown;
    const rule: TcbRule =
      typeof configured === "string"
        ? (configured.toLowerCase() as TcbRule)
        : isPlainObject(configured) && typeof configured.mode === "string"
          ? (configured.mode.toLowerCase() as TcbRule)
          : defaultRule(candidateValue);
    if (rule === "ignore") continue;
    if (rule === "incomparable") {
      sawIncomparable = true;
      continue;
    }
    if (requireExactComponentSet && !allowExtraComponents) {
      sawIncomparable = true;
    }
  }

  if (sawIncomparable) return "incomparable";
  if (sawOlder) return "older";
  return sawNewer ? "newer" : "equal";
}

export function isTcbAtLeastAsNew(
  candidate: TcbVector | undefined | null,
  baseline: TcbVector | undefined | null,
  options: TcbCompareOptions = {},
): boolean {
  const comparison = compareTcbVectors(candidate, baseline, options);
  return comparison === "newer" || comparison === "equal";
}

/** Aliases keep the package friendly to differently named host adapters. */
export const compareTcb = compareTcbVectors;
export const compareTcbVersionVectors = compareTcbVectors;
export const tcbAtLeast = isTcbAtLeastAsNew;
export const isTcbNewerOrEqual = isTcbAtLeastAsNew;
export const tcbCompatible = isTcbAtLeastAsNew;
