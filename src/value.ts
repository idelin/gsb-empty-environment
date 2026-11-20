import type { SqlValue } from "./types.ts";

export const NULL: SqlValue = Object.freeze({ kind: "null" } as const);

export function enumValue(value: string | number | boolean): SqlValue {
  return Object.freeze({ kind: "enum", value: String(value) } as const);
}

export function idValue(name: string): SqlValue {
  return Object.freeze({ kind: "id", name } as const);
}

export function nullValue(): SqlValue {
  return NULL;
}

export function isNullValue(value: SqlValue): boolean {
  return value.kind === "null";
}

export function equalValues(left: SqlValue, right: SqlValue): boolean | null {
  if (left.kind === "null" || right.kind === "null") return null;
  if (left.kind !== right.kind) return false;
  if (left.kind === "enum" && right.kind === "enum") {
    return left.value === right.value;
  }
  if (left.kind === "id" && right.kind === "id") {
    return left.name === right.name;
  }
  return false;
}

export function normalizeValue(raw: unknown): SqlValue {
  if (raw === null || raw === undefined) return NULL;
  if (typeof raw === "string") return enumValue(raw);
  if (typeof raw === "number" || typeof raw === "boolean") {
    return enumValue(raw);
  }
  if (typeof raw !== "object") return enumValue(String(raw));

  const record = raw as Record<string, unknown>;
  const kind = record.kind;
  if (kind === "null" || record.isNull === true) return NULL;
  if (kind === "enum" || kind === "literal") {
    const value = record.value;
    if (value === null || value === undefined) return NULL;
    return enumValue(String(value));
  }
  if (kind === "id" || kind === "identifier") {
    return idValue(String(record.name ?? record.value));
  }
  if (kind === "const") return normalizeValue(record.value);

  if (typeof record.value === "string") return enumValue(record.value);
  if (record.name !== undefined) return idValue(String(record.name));
  return enumValue(JSON.stringify(raw));
}

export function valueKey(value: SqlValue): string {
  if (value.kind === "null") return "n";
  if (value.kind === "enum") return `e:${JSON.stringify(value.value)}`;
  return `i:${JSON.stringify(value.name)}`;
}

export function valueFromKey(key: string): SqlValue {
  if (key === "n") return NULL;
  if (key.startsWith("e:")) return enumValue(JSON.parse(key.slice(2)));
  if (key.startsWith("i:")) return idValue(JSON.parse(key.slice(2)));
  throw new Error(`Invalid value key: ${key}`);
}

export function valueForDisplay(value: SqlValue): string {
  if (value.kind === "null") return "NULL";
  if (value.kind === "enum") return JSON.stringify(value.value);
  return `#${value.name}`;
}
