import type { RecordData } from "./types.ts";

const objectTag = "object";

function canonicalEntries(value: Readonly<Record<string, unknown>>, seen: Set<object>): string {
  if (seen.has(value)) {
    return '"<circular>"';
  }

  seen.add(value);
  const entries = Object.keys(value)
    .sort()
    .map((key) => `${JSON.stringify(key)}:${canonicalizeInternal(value[key], seen)}`);
  seen.delete(value);
  return `{${entries.join(",")}}`;
}

function canonicalizeInternal(value: unknown, seen: Set<object>): string {
  if (value === undefined) {
    return "undefined";
  }
  if (value === null) {
    return "null";
  }
  if (value instanceof Date) {
    return `date:${Number.isNaN(value.getTime()) ? "invalid" : value.toISOString()}`;
  }
  if (typeof value === "number") {
    if (Number.isNaN(value)) {
      return "number:NaN";
    }
    if (Object.is(value, -0)) {
      return "number:-0";
    }
    return `number:${String(value)}`;
  }
  if (typeof value === "bigint") {
    return `bigint:${value.toString()}`;
  }
  if (typeof value === "boolean") {
    return `boolean:${value ? "true" : "false"}`;
  }
  if (typeof value === "string") {
    return `string:${JSON.stringify(value)}`;
  }
  if (typeof value === "symbol") {
    return `symbol:${String(value.description ?? "")}`;
  }
  if (typeof value === "function") {
    return "function";
  }
  if (Array.isArray(value)) {
    return `array:[${value.map((entry) => canonicalizeInternal(entry, seen)).join(",")}]`;
  }
  if (value instanceof Map) {
    const entries = [...value.entries()]
      .map(([key, entry]) => `${canonicalizeInternal(key, seen)}=>${canonicalizeInternal(entry, seen)}`)
      .sort();
    return `map:{${entries.join(",")}}`;
  }
  if (value instanceof Set) {
    const entries = [...value.values()].map((entry) => canonicalizeInternal(entry, seen)).sort();
    return `set:{${entries.join(",")}}`;
  }
  if (typeof value === "object") {
    return `${objectTag}:${canonicalEntries(value as Readonly<Record<string, unknown>>, seen)}`;
  }
  return `${typeof value}:${String(value)}`;
}

/** Canonical, type-tagged representation used for stable equality and grouping. */
export function canonicalize(value: unknown): string {
  return canonicalizeInternal(value, new Set<object>());
}

/**
 * Quasi-identifier canonicalization. Unlike generic canonicalization,
 * `null` and `undefined` collapse to one missing-value token so that records
 * with differently represented missing cells share an equivalence class.
 */
export function canonicalizeQiValue(value: unknown): string {
  return isMissing(value) ? "missing" : canonicalize(value);
}

export function signatureForColumns(
  columns: readonly string[],
  values: Readonly<Record<string, unknown>>,
): string {
  const parts = columns.map(
    (column) => `${JSON.stringify(column)}:${canonicalizeQiValue(values[column])}`,
  );
  return `{${parts.join(",")}}`;
}

/** Position-based signature for joins where column names can differ between datasets. */
export function signatureForValues(values: readonly unknown[]): string {
  return `[${values.map((value) => canonicalizeQiValue(value)).join(",")}]`;
}

export function valuesForColumns(
  record: RecordData,
  columns: readonly string[],
): Record<string, unknown> {
  const values: Record<string, unknown> = {};
  for (const column of columns) {
    values[column] = record[column];
  }
  return values;
}

export function isMissing(value: unknown): boolean {
  return value === undefined || value === null;
}

export function assertNonEmptyUniqueColumns(columns: readonly string[], label: string): void {
  if (columns.length === 0) {
    throw new Error(`${label} must contain at least one column`);
  }
  const seen = new Set<string>();
  for (const column of columns) {
    if (typeof column !== "string" || column.length === 0) {
      throw new Error(`${label} contains an invalid column name`);
    }
    if (seen.has(column)) {
      throw new Error(`${label} contains duplicate column "${column}"`);
    }
    seen.add(column);
  }
}

export function assertThreshold(threshold: number): void {
  if (!Number.isFinite(threshold) || threshold < 1) {
    throw new Error(`threshold must be a finite number greater than or equal to 1, received ${String(threshold)}`);
  }
}
