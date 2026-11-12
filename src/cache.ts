import {
  cacheKey,
  OPAQUE_ORIGIN,
  originOf,
  sameOrigin,
  type Origin,
} from "./origin.ts";
import { integritySatisfied, normalizeIntegrity } from "./integrity.ts";
import type {
  CacheEntryInput,
  CachedResponse,
  CredentialsMode,
  MatchResult,
  StructuredRequest,
} from "./types.ts";

export interface ResponseCacheOptions {
  /**
   * Logical cache/schema version. Reads only return entries stamped with the
   * same version, so bumping this isolates stale entries ("version switch").
   */
  version: string;
}

interface StoredEntry {
  key: string;
  response: CachedResponse;
}

/**
 * In-memory response cache enforcing the Fetch security model.
 *
 * Every stored entry carries its origin, response type, credentials mode and
 * integrity state. A read succeeds only when *all* of those attributes are
 * compatible with the request. Missing metadata is normalized to
 * untrusted/unverified, never silently upgraded.
 */
export class ResponseCache {
  readonly version: string;
  private readonly entries: Map<string, StoredEntry>;

  constructor(options: ResponseCacheOptions, sharedEntries?: Map<string, StoredEntry>) {
    if (!options.version || typeof options.version !== "string") {
      throw new TypeError("ResponseCache requires a non-empty version string");
    }
    this.version = options.version;
    this.entries = sharedEntries ?? new Map<string, StoredEntry>();
  }

  /** Number of entries currently held (all versions included). */
  get size(): number {
    return this.entries.size;
  }

  /**
   * Store a response. Throws on responses that cannot be cached at all
   * (network errors, opaque redirects in manual mode) or on contradictory
   * security metadata. Never assumes missing metadata means "trusted".
   */
  put<T>(input: CacheEntryInput<T>): CachedResponse<T> {
    const request = normalizeRequest(input.request);
    const type = input.type;

    if (type === "error") {
      throw new TypeError("error responses are not cacheable");
    }
    if (type === "opaqueredirect" && request.redirect !== "manual") {
      throw new TypeError(
        "opaqueredirect responses can only be cached for manual redirect requests",
      );
    }

    const credentialsMode = input.credentialsMode;
    if (
      credentialsMode !== "omit" &&
      credentialsMode !== "same-origin" &&
      credentialsMode !== "include"
    ) {
      throw new TypeError(`invalid credentials mode: ${String(credentialsMode)}`);
    }

    // no-cors cross-origin fetches can only produce opaque responses.
    if (request.mode === "no-cors" && type !== "opaque") {
      throw new TypeError(
        "no-cors requests can only cache opaque responses",
      );
    }

    const finalUrl = input.responseUrl ?? request.url;
    const key = cacheKey(request.url);
    // Opaque / opaque-redirect filtered responses carry an opaque origin and
    // an empty URL list in the Fetch model, even though the cache key stays
    // the request URL.
    const opaqueFiltered = type === "opaque" || type === "opaqueredirect";
    const origin: Origin = opaqueFiltered ? OPAQUE_ORIGIN : originOf(finalUrl);

    const integrity = opaqueFiltered
      ? { state: "unchecked" as const, metadata: [] }
      : normalizeIntegrity({
        state: input.integrityState,
        metadata: input.integrityMetadata,
      });

    // Opaque responses expose no headers/body and can never carry integrity.
    if (opaqueFiltered) {
      if (Object.keys(input.headers ?? {}).length > 0 || input.body !== undefined) {
        throw new TypeError(
          "opaque/opaqueredirect responses must not expose headers or body",
        );
      }
    }

    if (!opaqueFiltered && origin === OPAQUE_ORIGIN) {
      throw new TypeError("non-opaque response must carry a tuple origin");
    }

    const response: CachedResponse<T> = {
      url: opaqueFiltered ? "" : cacheKey(finalUrl),
      status: type === "opaque" ? 0 : input.status,
      statusText: input.statusText,
      headers: { ...(input.headers ?? {}) },
      body: (opaqueFiltered ? undefined : input.body) as T,
      origin,
      type,
      credentialsMode,
      integrityState: integrity.state,
      integrityMetadata: integrity.metadata,
      cacheVersion: this.version,
    };

    // Overwrite never inherits the previous entry's trust attributes.
    this.entries.set(key, { key, response: response as CachedResponse });
    return response;
  }

  /**
   * Look up a response with full security-attribute matching. Prefer this
   * over {@link match} when the rejection reason matters for logging.
   */
  inspect<T = unknown>(request: StructuredRequest): MatchResult<T> {
    const normalized = normalizeRequest(request);
    const key = cacheKey(normalized.url);
    const stored = this.entries.get(key);
    if (!stored) {
      return { matched: false, reason: "no-entry" };
    }

    const entry = stored.response as CachedResponse<T>;

    if (entry.cacheVersion !== this.version) {
      return {
        matched: false,
        reason: "version",
        detail: `entry=${entry.cacheVersion} cache=${this.version}`,
      };
    }

    if (entry.type === "error") {
      return { matched: false, reason: "error-response" };
    }

    if (normalized.redirect === "manual") {
      if (entry.type !== "opaqueredirect") {
        return {
          matched: false,
          reason: "redirect-mode",
          detail: "manual redirect request can only reuse an opaqueredirect entry",
        };
      }
      if (!credentialsCompatible(normalized.credentials, entry.credentialsMode)) {
        return {
          matched: false,
          reason: "credentials-mode",
          detail: `request=${normalized.credentials} entry=${entry.credentialsMode}`,
        };
      }
      return { matched: true, response: entry };
    }

    if (
      normalized.redirect === "error" &&
      entry.type !== "opaqueredirect" &&
      entry.url !== key
    ) {
      return {
        matched: false,
        reason: "redirect-mode",
        detail: "redirect:error request cannot reuse a redirected response",
      };
    }

    // Opaque entries carry an opaque origin and are only reusable by no-cors
    // requests at the same URL; they must never be served as verified
    // same-origin or cross-origin content.
    if (entry.type === "opaque") {
      if (normalized.mode !== "no-cors") {
        return {
          matched: false,
          reason: "opaque-for-cross-origin",
          detail: "opaque response cannot be served outside no-cors mode",
        };
      }
      if (!credentialsCompatible(normalized.credentials, entry.credentialsMode)) {
        return {
          matched: false,
          reason: "credentials-mode",
          detail: `request=${normalized.credentials} entry=${entry.credentialsMode}`,
        };
      }
      if (normalized.requireIntegrity) {
        return {
          matched: false,
          reason: "integrity",
          detail: "opaque entries have no verifiable integrity metadata",
        };
      }
      return { matched: true, response: entry };
    }

    if (entry.origin === OPAQUE_ORIGIN || !sameOrigin(safeOrigin(entry.url), entry.origin)) {
      return {
        matched: false,
        reason: "response-type",
        detail: "stored entry has inconsistent origin metadata",
      };
    }

    if (!credentialsCompatible(normalized.credentials, entry.credentialsMode)) {
      return {
        matched: false,
        reason: "credentials-mode",
        detail: `request=${normalized.credentials} entry=${entry.credentialsMode}`,
      };
    }

    if (!integritySatisfied(entry.integrityState, normalized.requireIntegrity === true)) {
      return {
        matched: false,
        reason: "integrity",
        detail: `entry integrity state is ${entry.integrityState}`,
      };
    }

    // Cross-origin requests may only consume cors/opaque-redirect-filtered
    // responses; basic responses are same-origin-only.
    const requestOrigin = safeOrigin(normalized.url);
    const crossOrigin = !sameOrigin(requestOrigin, entry.origin);
    if (crossOrigin && entry.type === "basic") {
      return {
        matched: false,
        reason: "response-type",
        detail: "basic (same-origin) response reused cross-origin",
      };
    }
    if (normalized.mode === "same-origin" && crossOrigin) {
      return {
        matched: false,
        reason: "response-type",
        detail: "same-origin mode request matched cross-origin entry",
      };
    }
    if (normalized.mode === "no-cors" && entry.type !== "opaque") {
      return {
        matched: false,
        reason: "response-type",
        detail: "no-cors request can only reuse opaque responses",
      };
    }

    return { matched: true, response: entry };
  }

  /** Return the cached response or `undefined` when any attribute mismatches. */
  match<T = unknown>(request: StructuredRequest): CachedResponse<T> | undefined {
    const result = this.inspect<T>(request);
    return result.matched ? result.response : undefined;
  }

  delete(requestUrl: string): boolean {
    return this.entries.delete(cacheKey(requestUrl));
  }

  /** Snapshot of stored entries, including stale versions for diagnostics. */
  entriesSnapshot(): readonly CachedResponse[] {
    return Array.from(this.entries.values(), (entry) => entry.response);
  }

  /**
   * Return a cache view at `newVersion` over the same storage. Existing
   * entries become version-stale and stop matching until rewritten.
   */
  switchVersion(newVersion: string): ResponseCache {
    if (!newVersion || typeof newVersion !== "string") {
      throw new TypeError("switchVersion requires a non-empty version string");
    }
    return new ResponseCache({ version: newVersion }, this.entries);
  }
}

interface NormalizedRequest {
  url: string;
  mode: NonNullable<StructuredRequest["mode"]>;
  redirect: NonNullable<StructuredRequest["redirect"]>;
  credentials: NonNullable<StructuredRequest["credentials"]>;
  requireIntegrity: boolean;
}

function normalizeRequest(request: StructuredRequest): NormalizedRequest {
  if (!request || typeof request.url !== "string") {
    throw new TypeError("request.url is required");
  }
  // Validate eagerly so malformed URLs never become keys.
  cacheKey(request.url);
  return {
    url: request.url,
    mode: request.mode ?? "cors",
    redirect: request.redirect ?? "follow",
    credentials: request.credentials ?? "same-origin",
    requireIntegrity: request.requireIntegrity === true,
  };
}

function safeOrigin(rawUrl: string): Origin {
  try {
    return originOf(rawUrl);
  } catch {
    return OPAQUE_ORIGIN;
  }
}

/**
 * Credentials compatibility matrix:
 * - anonymous (`omit`) reads never reuse entries fetched with credentials
 * - `same-origin` reads reuse omit/same-origin entries, never `include`
 * - `include` reads reuse entries fetched under the same mode only, so an
 *   anonymous cached entry cannot answer a cookie-bearing request
 */
function credentialsCompatible(
  requestMode: CredentialsMode,
  entryMode: CredentialsMode,
): boolean {
  switch (requestMode) {
    case "omit":
      return entryMode === "omit";
    case "same-origin":
      return entryMode === "omit" || entryMode === "same-origin";
    case "include":
      return entryMode === "include";
    default: {
      const exhaustive: never = requestMode;
      return exhaustive;
    }
  }
}
