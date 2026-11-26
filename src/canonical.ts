/**
 * Deterministic JSON canonicalisation (JCS-style) plus the byte encodings used
 * by content addressing and the manifest envelope.
 *
 * Rules:
 *  - object keys are emitted in lexicographic (UTF-16 code unit) order;
 *  - arrays preserve order;
 *  - JSON number formatting follows ECMAScript Number::toString;
 *  - duplicate keys are rejected up front so canonical bytes are unambiguous;
 *  - `undefined`, functions and symbols are never silently dropped: they make
 *    the value non-canonicalisable and produce a hard error.
 */

import { ContextResolutionError } from "./types.ts";
import type { ContextDiagnostic, ErrorCode } from "./types.ts";

function diagnostic(
  code: ErrorCode,
  message: string,
  detail = "",
): ContextDiagnostic {
  return {
    code,
    message,
    offendingTerm: "",
    offendingCid: "",
    importPath: [],
    depth: 0,
    detail,
  };
}

export function fail(
  code: ErrorCode,
  message: string,
  detail = "",
): never {
  throw new ContextResolutionError(diagnostic(code, message, detail));
}

/** Deep structural validation used before canonicalisation. */
export function assertCanonicalisable(value: unknown, path = "$"): void {
  if (value === null) return;
  const t = typeof value;
  if (t === "string" || t === "boolean") return;
  if (t === "number") {
    if (!Number.isFinite(value as number)) {
      fail("CONTENT_MALFORMED", "Non-finite number cannot be canonicalised", path);
    }
    return;
  }
  if (t === "bigint") {
    fail("CONTENT_MALFORMED", "bigint values are not valid JSON-LD content", path);
  }
  if (t === "undefined" || t === "function" || t === "symbol") {
    fail("CONTENT_MALFORMED", `Non-serialisable value of kind ${t}`, path);
  }
  if (Array.isArray(value)) {
    value.forEach((item, index) => assertCanonicalisable(item, `${path}[${index}]`));
    return;
  }
  if (t === "object") {
    const record = value as Record<string, unknown>;
    const seen = new Set<string>();
    for (const key of Object.keys(record)) {
      if (seen.has(key)) {
        fail("CONTENT_MALFORMED", "Duplicate object key in content", `${path}.${key}`);
      }
      seen.add(key);
      assertCanonicalisable(record[key], `${path}.${key}`);
    }
    return;
  }
  fail("CONTENT_MALFORMED", "Value has unsupported JSON type", `${path} :: ${t}`);
}

/**
 * Serialise to deterministic JSON. Keys sort by UTF-16 code unit order, which
 * matches `Array.prototype.sort` default ordering for BMP and surrogate pairs.
 */
export function canonicalJson(value: unknown): string {
  assertCanonicalisable(value);
  return encodeCanonical(value);
}

function encodeCanonical(value: unknown): string {
  if (value === null) return "null";
  if (typeof value === "string") return JSON.stringify(value);
  if (typeof value === "number" || typeof value === "boolean") {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map(encodeCanonical).join(",")}]`;
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  const parts = keys.map((key) => `${JSON.stringify(key)}:${encodeCanonical(record[key])}`);
  return `{${parts.join(",")}}`;
}

const encoder = new TextEncoder();

export function canonicalBytes(value: unknown): Uint8Array {
  return encoder.encode(canonicalJson(value));
}

export function toHex(bytes: Uint8Array): string {
  let out = "";
  for (const byte of bytes) {
    out += byte.toString(16).padStart(2, "0");
  }
  return out;
}

export function fromHex(hex: string): Uint8Array {
  if (hex.length % 2 !== 0 || /[^0-9a-fA-F]/.test(hex)) {
    fail("TRUST_MANIFEST_MALFORMED", "Malformed hexadecimal digest", hex);
  }
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i += 1) {
 out[i] = Number.parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}

export function base64UrlEncode(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}

export function base64UrlDecode(encoded: string): Uint8Array {
  if (!/^[A-Za-z0-9_-]*$/.test(encoded)) {
    fail("TRUST_MANIFEST_MALFORMED", "Malformed base64url value", encoded.slice(0, 32));
  }
  const padded = encoded.replaceAll("-", "+").replaceAll("_", "/");
  const fill = padded.length % 4 === 0 ? "" : "=".repeat(4 - (padded.length % 4));
  const binary = atob(padded + fill);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) out[i] = binary.charCodeAt(i);
  return out;
}

/** Constant-time hex comparison to avoid digest oracle timing channels. */
export function constantTimeEqualHex(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i += 1) {
    diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  }
  return diff === 0;
}
