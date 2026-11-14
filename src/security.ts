export const HARD_LIMITS = Object.freeze({
  maxOrigins: 64,
  maxOriginLength: 512,
  maxOperationLength: 32,
  maxRequestIdLength: 96,
  maxSessionIdLength: 64,
  minPageMessageBytes: 256,
  maxPageMessageBytes: 64 * 1024,
  minPageStringLength: 16,
  maxPageStringLength: 8 * 1024,
  maxPathDepth: 8,
  maxPathTagLength: 32,
});

export type ValidationCode =
  | "invalid_type"
  | "invalid_shape"
  | "invalid_value"
  | "unknown_key"
  | "prototype_key"
  | "accessor_property"
  | "too_long"
  | "too_short"
  | "too_many"
  | "duplicate"
  | "not_allowed";

export class ValidationError extends Error {
  readonly code: ValidationCode;

  constructor(code: ValidationCode, detail: string) {
    super(`${code}: ${detail}`);
    this.name = "ValidationError";
    this.code = code;
  }
}

export function reject(code: ValidationCode, detail: string): never {
  throw new ValidationError(code, detail);
}

const FORBIDDEN_KEYS = new Set(["__proto__", "prototype", "constructor"]);

export function isDataRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function requireDataRecord(
  value: unknown,
  label: string,
): Record<string, unknown> {
  if (!isDataRecord(value)) {
    reject("invalid_type", `${label} must be an object`);
  }

  if (Object.getOwnPropertySymbols(value).length !== 0) {
    reject("invalid_shape", `${label} must not contain symbol keys`);
  }

  for (const name of Object.getOwnPropertyNames(value)) {
    if (FORBIDDEN_KEYS.has(name)) {
      reject("prototype_key", `${label} contains a forbidden key`);
    }
  }

  return value;
}

export function ownDataProperty(
  value: Record<string, unknown>,
  key: string,
  label: string,
): unknown {
  const descriptor = Object.getOwnPropertyDescriptor(value, key);
  if (descriptor === undefined) {
    reject("invalid_shape", `${label} is missing ${key}`);
  }
  if (!("value" in descriptor)) {
    reject("accessor_property", `${label}.${key} must be a data property`);
  }
  return descriptor.value;
}

export function hasOwn(
  value: Record<string, unknown>,
  key: string,
): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

/**
 * Copies only named own data properties into a null-prototype object. This
 * deliberately avoids object spread and assignment through __proto__.
 */
export function exactRecord(
  value: unknown,
  allowedKeys: readonly string[],
  label: string,
): Record<string, unknown> {
  const record = requireDataRecord(value, label);
  const allowed = new Set(allowedKeys);
  const output = Object.create(null) as Record<string, unknown>;

  for (const name of Object.getOwnPropertyNames(record)) {
    if (!allowed.has(name)) {
      reject("unknown_key", `${label} contains an unknown key`);
    }
    const descriptor = Object.getOwnPropertyDescriptor(record, name);
    if (
      descriptor === undefined ||
      !("value" in descriptor) ||
      !descriptor.enumerable
    ) {
      reject("invalid_shape", `${label}.${name} must be enumerable data`);
    }
    output[name] = descriptor.value;
  }

  return output;
}

export function boundedString(
  value: unknown,
  label: string,
  minimum: number,
  maximum: number,
  maximumBytes = Number.POSITIVE_INFINITY,
): string {
  if (typeof value !== "string") {
    reject("invalid_type", `${label} must be a string`);
  }
  if (value.length < minimum) {
    reject("too_short", `${label} is too short`);
  }
  if (value.length > maximum) {
    reject("too_long", `${label} is too long`);
  }
  if (byteLength(value) > maximumBytes) {
    reject("too_long", `${label} is too long`);
  }
  return value;
}

export function boundedInteger(
  value: unknown,
  label: string,
  minimum: number,
  maximum: number,
): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value)) {
    reject("invalid_type", `${label} must be an integer`);
  }
  if (value < minimum || value > maximum) {
    reject("invalid_value", `${label} is outside its allowed range`);
  }
  return value;
}

export function boundedStringArray(
  value: unknown,
  label: string,
  options: Readonly<{
    maxItems: number;
    minLength: number;
    maxLength: number;
    maxBytes?: number;
  }>,
): readonly string[] {
  if (!Array.isArray(value)) {
    reject("invalid_type", `${label} must be an array`);
  }
  if (value.length > options.maxItems) {
    reject("too_many", `${label} has too many entries`);
  }
  if (Object.getOwnPropertySymbols(value).length !== 0) {
    reject("invalid_shape", `${label} must not contain symbol keys`);
  }

  for (const name of Object.getOwnPropertyNames(value)) {
    if (name === "length") {
      continue;
    }
    if (!/^(0|[1-9][0-9]*)$/.test(name) || Number(name) >= value.length) {
      reject("invalid_shape", `${label} must be a dense array`);
    }
  }

  const output: string[] = [];
  const seen = new Set<string>();
  for (let index = 0; index < value.length; index += 1) {
    const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
    if (descriptor === undefined || !("value" in descriptor)) {
      reject("invalid_shape", `${label} must be a dense array`);
    }
    const entry = boundedString(
      descriptor.value,
      `${label}[${index}]`,
      options.minLength,
      options.maxLength,
      options.maxBytes,
    );
    if (seen.has(entry)) {
      reject("duplicate", `${label} contains duplicate entries`);
    }
    seen.add(entry);
    output.push(entry);
  }

  return Object.freeze(output);
}

export function parseCanonicalOrigin(
  value: unknown,
  label: string,
): string {
  const text = boundedString(
    value,
    label,
    1,
    HARD_LIMITS.maxOriginLength,
  );
  if (text === "*" || text === "null" || text.includes("\\")) {
    reject("invalid_value", `${label} is not a concrete origin`);
  }

  let url: URL;
  try {
    url = new URL(text);
  } catch {
    reject("invalid_value", `${label} is not a URL`);
  }

  if (url.protocol !== "https:" && url.protocol !== "http:") {
    reject("invalid_value", `${label} must use HTTP or HTTPS`);
  }
  if (url.username !== "" || url.password !== "") {
    reject("invalid_value", `${label} must not contain credentials`);
  }
  if (url.search !== "" || url.hash !== "") {
    reject("invalid_value", `${label} must not contain a query or fragment`);
  }
  if (url.pathname !== "/" && url.pathname !== "") {
    reject("invalid_value", `${label} must be an origin, not a URL path`);
  }
  if (url.origin === "null" || text !== url.origin) {
    reject("invalid_value", `${label} must be a canonical origin`);
  }
  return url.origin;
}

export function parseHostPermissionOrigin(
  value: unknown,
  label: string,
): string {
  const text = boundedString(
    value,
    label,
    1,
    HARD_LIMITS.maxOriginLength,
  );
  if (text.includes("\\")) {
    reject("invalid_value", `${label} is not a URL`);
  }

  let url: URL;
  try {
    url = new URL(text);
  } catch {
    reject("invalid_value", `${label} is not a URL`);
  }
  if (url.pathname !== "/" && url.pathname !== "/*") {
    reject("invalid_value", `${label} must be an origin permission`);
  }
  if (url.hostname.includes("*")) {
    reject(
      "invalid_value",
      `${label} must name an exact host; wildcards are not accepted`,
    );
  }
  if (url.search !== "" || url.hash !== "") {
    reject("invalid_value", `${label} must not contain a query or fragment`);
  }
  return parseCanonicalOrigin(url.origin, label);
}

export function byteLength(value: string): number {
  return new TextEncoder().encode(value).byteLength;
}

export function randomId(byteCount = 16): string {
  if (!Number.isSafeInteger(byteCount) || byteCount < 8 || byteCount > 64) {
    throw new Error("invalid random identifier size");
  }
  const bytes = new Uint8Array(byteCount);
  globalThis.crypto.getRandomValues(bytes);

  let output = "";
  for (const byte of bytes) {
    output += byte.toString(16).padStart(2, "0");
  }
  return output;
}

export function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === "object") {
    for (const name of Object.getOwnPropertyNames(value)) {
      const descriptor = Object.getOwnPropertyDescriptor(value, name);
      if (descriptor !== undefined && "value" in descriptor) {
        deepFreeze(descriptor.value);
      }
    }
    Object.freeze(value);
  }
  return value;
}

export function hasControlCharacters(value: string): boolean {
  return /[\u0000-\u001f\u007f]/.test(value);
}

export function isOriginInList(
  origin: string,
  allowedOrigins: readonly string[],
): boolean {
  for (const allowed of allowedOrigins) {
    if (allowed === origin) {
      return true;
    }
  }
  return false;
}
