/**
 * Structured request/response model for the offline response cache.
 *
 * These types intentionally mirror the security-relevant fields of the
 * Fetch standard rather than wrapping platform `Request`/`Response`
 * objects, so the package can be embedded in any TypeScript host
 * (service worker, worker thread, native runtime).
 */

import type { Origin } from "./origin.ts";

/** Fetch response type, matching the `Response.type` vocabulary. */
export type ResponseType =
  | "basic"
  | "cors"
  | "error"
  | "opaque"
  | "opaqueredirect";

/** Fetch request credentials mode. */
export type CredentialsMode = "omit" | "same-origin" | "include";

/** Fetch request mode. */
export type RequestMode =
  | "same-origin"
  | "cors"
  | "no-cors"
  | "navigate"
  | "websocket";

/** Redirect handling mode of the request. */
export type RedirectMode = "follow" | "error" | "manual";

/**
 * Validation state of Subresource Integrity metadata attached to a response:
 * - `verified`: digest metadata present and matched the response body
 * - `unchecked`: no integrity metadata or no validation performed
 * - `mismatch`: metadata present but the body failed the check
 */
export type IntegrityState = "verified" | "unchecked" | "mismatch";

/** One SRI record, e.g. parsed from `sha384-...` integrity syntax. */
export interface IntegrityMetadata {
  algorithm: "sha256" | "sha384" | "sha512";
  digest: string;
}

/** Security attributes carried by every cached response. */
export interface ResponseSecurityInfo {
  /** Final origin of the response (after any redirects). */
  origin: Origin;
  type: ResponseType;
  credentialsMode: CredentialsMode;
  integrityState: IntegrityState;
  integrityMetadata: IntegrityMetadata[];
  /** Logical cache/schema version the entry was written under. */
  cacheVersion: string;
}

/**
 * A structured, platform-independent cached response. `body` is opaque to
 * the cache implementation; hosts may store `Uint8Array`, strings or any
 * serializable payload.
 */
export interface CachedResponse<T = unknown> extends ResponseSecurityInfo {
  /** Final response URL (may differ from the request URL after redirects). */
  url: string;
  status: number;
  statusText?: string;
  headers: Readonly<Record<string, string>>;
  body: T;
}

/** Structured request used as both the write key and the read matcher. */
export interface StructuredRequest {
  url: string;
  /** @default "cors" */
  mode?: RequestMode;
  /** @default "follow" */
  redirect?: RedirectMode;
  /** @default "same-origin" */
  credentials?: CredentialsMode;
  /**
   * When true, only responses whose integrity state is `verified` may be
   * served. Mirrors a request carrying an `integrity` descriptor.
   */
  requireIntegrity?: boolean;
}

/** Host-provided response payload accepted by {@link ResponseCache.put}. */
export interface CacheEntryInput<T = unknown> {
  request: StructuredRequest;
  /**
   * Final response URL. Defaults to the request URL when omitted; it differs
   * when the fetch followed cross-origin redirects.
   */
  responseUrl?: string;
  status: number;
  statusText?: string;
  headers?: Record<string, string>;
  body?: T;
  type: ResponseType;
  /**
   * Credentials mode the response was actually fetched with. Must be
   * supplied explicitly; the cache never guesses a trusted default.
   */
  credentialsMode: CredentialsMode;
  integrityState?: IntegrityState;
  integrityMetadata?: IntegrityMetadata[];
}

/** Reason returned by {@link ResponseCache.match} diagnostics. */
export interface MissDiagnostics {
  matched: false;
  reason:
  | "no-entry"
  | "version"
  | "opaque-for-cross-origin"
  | "redirect-mode"
  | "response-type"
  | "credentials-mode"
  | "integrity"
  | "error-response";
  detail?: string;
}

export interface HitDiagnostics<T> {
  matched: true;
  response: CachedResponse<T>;
}

export type MatchResult<T> = HitDiagnostics<T> | MissDiagnostics;
