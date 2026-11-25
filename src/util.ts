export type Instant = string | number | Date;

export const MIN_TIME = Number.NEGATIVE_INFINITY;
export const MAX_TIME = Number.POSITIVE_INFINITY;

export function toMillis(value: Instant | undefined, fallback: number): number {
  if (value === undefined || value === null) return fallback;
  if (value instanceof Date) {
    const ms = value.getTime();
    if (!Number.isFinite(ms)) throw new TypeError("invalid Date instant");
    return ms;
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) return value === Infinity ? MAX_TIME : MIN_TIME;
    return value;
  }
  if (typeof value === "string") {
    const trimmed = value.trim();
    if (trimmed === "") throw new TypeError("empty instant string");
    const ms = Date.parse(trimmed);
    if (!Number.isFinite(ms)) {
      throw new TypeError(`invalid instant: ${value}`);
    }
    return ms;
  }
  throw new TypeError(`invalid instant: ${String(value)}`);
}

export function finiteOrNull(value: number): number | null {
  return Number.isFinite(value) ? value : null;
}

export function stableStringify(value: unknown): string {
  return stringifyValue(value, new WeakSet<object>());
}

function stringifyValue(value: unknown, seen: WeakSet<object>): string {
  if (value === null) return "null";
  if (value === undefined) return '"<undefined>"';
  if (typeof value === "number") {
    if (Number.isNaN(value)) return '"<NaN>"';
    if (value === Infinity) return '"<Infinity>"';
    if (value === -Infinity) return '"<-Infinity>"';
    return Object.is(value, -0) ? "0" : String(value);
  }
  if (typeof value === "bigint") return `"${value.toString()}n"`;
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "string") return JSON.stringify(value);
  if (Array.isArray(value)) {
    return `[${value.map((item) => stringifyValue(item, seen)).join(",")}]`;
  }
  if (typeof value === "object") {
    if (seen.has(value as object)) return '"<cycle>"';
    seen.add(value as object);
    const record = value as Record<string, unknown>;
    const keys = Object.keys(record).sort();
    const body = keys
      .filter((key) => record[key] !== undefined)
      .map((key) => `${JSON.stringify(key)}:${stringifyValue(record[key], seen)}`)
      .join(",");
    seen.delete(value as object);
    return `{${body}}`;
  }
  return JSON.stringify(String(value));
}

export function stableDigest(value: unknown): string {
  const text = stableStringify(value);
  let h1 = 0x811c9dc5;
  let h2 = 0x9e3779b9;
  for (let index = 0; index < text.length; index += 1) {
    const code = text.charCodeAt(index);
    h1 ^= code;
    h1 = Math.imul(h1, 0x01000193) >>> 0;
    h2 ^= code + index;
    h2 = Math.imul(h2, 0x85ebca6b) >>> 0;
    h2 = (h2 ^ (h2 >>> 13)) >>> 0;
  }
  return `${h1.toString(16).padStart(8, "0")}${h2.toString(16).padStart(8, "0")}`;
}

export function uniqueSorted(values: Iterable<string>): string[] {
  return [...new Set(values)].sort();
}

export function intersection<T>(a: Iterable<T>, b: Iterable<T>): T[] {
  const right = new Set(b);
  const result: T[] = [];
  const seen = new Set<T>();
  for (const item of a) {
    if (right.has(item) && !seen.has(item)) {
      seen.add(item);
      result.push(item);
    }
  }
  return result;
}

export interface NormalizedEmail {
  email: string;
  local: string;
  domain: string;
  emailKey: string;
}

const DOMAIN_LABEL = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/;

export function normalizeDomain(raw: string): string {
  let domain = raw.trim().toLowerCase();
  if (domain.endsWith(".")) domain = domain.slice(0, -1);
  return domain;
}

export function normalizeEmail(raw: string): NormalizedEmail {
  let text = raw.trim();
  const angle = text.match(/<([^<>]+)>/);
  if (angle) text = angle[1].trim();
  const at = text.lastIndexOf("@");
  if (at < 0) {
    const domain = normalizeDomain(text);
    return {
      email: domain,
      local: "",
      domain,
      emailKey: `@${domain}`,
    };
  }
  const local = text.slice(0, at).trim();
  const domain = normalizeDomain(text.slice(at + 1));
  return {
    email: `${local.toLowerCase()}@${domain}`,
    local,
    domain,
    emailKey: `${local.toLowerCase()}@${domain}`,
  };
}

export function domainOfEmail(raw: string): string {
  return normalizeEmail(raw).domain;
}

export function isLikelyEmail(raw: string): boolean {
  const normalized = normalizeEmail(raw);
  return normalized.local.length > 0 && normalized.domain.length > 0;
}

export function isValidDomain(raw: string): boolean {
  const domain = normalizeDomain(raw);
  if (!domain || domain.length > 253) return false;
  const labels = domain.split(".");
  return labels.every((label) => label.length > 0 && label.length <= 63 && DOMAIN_LABEL.test(label));
}

export function compareStrings(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

export function compareStringArrays(a: string[], b: string[]): number {
  const length = Math.min(a.length, b.length);
  for (let index = 0; index < length; index += 1) {
    const order = compareStrings(a[index], b[index]);
    if (order !== 0) return order;
  }
  return a.length - b.length;
}

export function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value));
}

export function assertNever(value: never): never {
  throw new Error(`unexpected value: ${String(value)}`);
}
