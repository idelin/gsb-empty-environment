/**
 * Token-aware URL handling.
 *
 * The functions in this module deliberately treat parameter names, not values,
 * as the source of truth. Values are attacker-controlled and can be anything;
 * names such as `token`, `reset_token`, `accessToken`, `code` and `sig` are
 * what make a URL unsafe to copy into a share sheet or an external link.
 */

export interface SensitiveParamOptions {
  /** Additional parameter names to treat as sensitive. */
  extraNames?: Iterable<string>;
  /** Parameter names that should never be treated as sensitive. */
  allowNames?: Iterable<string>;
  /** Include common PII parameter names such as `email`. Defaults to true. */
  includePii?: boolean;
  /** Strip opaque JWT-looking fragments. Defaults to true. */
  stripOpaqueHashTokens?: boolean;
  /** Strip URL credentials (user:password@host). Defaults to false. */
  stripCredentials?: boolean;
  /** Keep the URL fragment untouched. Defaults to false. */
  keepHash?: boolean;
}

export interface ShareableUrlOptions extends SensitiveParamOptions {
  /** `drop` silently removes sensitive parameters; `throw` rejects them. */
  sensitiveParamBehavior?: "drop" | "throw";
}

const SENSITIVE_SEGMENTS = new Set([
  "token",
  "tokens",
  "secret",
  "secrets",
  "password",
  "passwords",
  "passwd",
  "pwd",
  "otp",
  "totp",
  "jwt",
  "nonce",
  "code",
  "ticket",
  "tickets",
  "signature",
  "sig",
  "session",
  "sessions",
  "sessionid",
  "session-id",
  "sid",
  "auth",
  "authorization",
  "apikey",
  "api-key",
  "csrf",
  "xsrf",
  "invite",
  "invitation",
  "magic",
  "verification",
  "confirm",
  "confirmation",
  "recovery",
  "recover",
  "unsubscribe",
  "grant",
  "assertion",
  "claim",
]);

const SENSITIVE_EXACT_NAMES = new Set([
  "key",
  "code",
  "state",
  "relay",
  "relaytoken",
  "relay-token",
  "download",
  "download-token",
  "share",
  "share-token",
  "private-key",
  "secret-key",
  "client-secret",
  "access-code",
  "authorization-code",
  "verification-code",
  "reset-code",
  "recovery-code",
  "one-time-code",
  "one-time-token",
  "magic-link",
  "magic-token",
  "email-token",
  "email-verification-token",
  "password-reset-token",
  "reset-token",
  "verify-token",
  "confirm-token",
  "bearer-token",
  "refresh-token",
  "access-token",
  "id-token",
  "auth-token",
  "oauth-state",
  "saml-response",
  "login-token",
  "device-token",
]);

const PII_SEGMENTS = new Set([
  "email",
  "e-mail",
  "mail",
  "phone",
  "telephone",
  "address",
  "ssn",
  "dob",
]);

const JWT_LIKE =
  /^[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}$/;
const OPAQUE_HASH_TOKEN = /^(?:bearer\s+)?(?:token|otp|jwt|code|key)[:=]/i;

function normalizeName(name: string): string {
  return String(name)
    .normalize("NFKC")
    .trim()
    .replace(/([a-z0-9])([A-Z])/g, "$1-$2")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

function hasSegment(name: string, set: Set<string>): boolean {
  if (set.has(name)) return true;
  for (const segment of name.split("-")) {
    if (set.has(segment)) return true;
  }
  return false;
}

function decodeParamName(rawName: string): string {
  try {
    return decodeURIComponent(rawName.replace(/\+/g, " "));
  } catch {
    return rawName;
  }
}

function hasSensitiveRawQuery(
  rawSearch: string,
  options: SensitiveParamOptions,
): boolean {
  const raw = rawSearch.startsWith("?") ? rawSearch.slice(1) : rawSearch;
  if (raw.length === 0) return false;
  for (const part of raw.split("&")) {
    if (part.length === 0) continue;
    const equals = part.indexOf("=");
    const rawName = equals === -1 ? part : part.slice(0, equals);
    if (isSensitiveParamName(decodeParamName(rawName), options)) return true;
  }
  return false;
}

function filterRawQuery(
  rawSearch: string,
  options: SensitiveParamOptions,
): string {
  const raw = rawSearch.startsWith("?") ? rawSearch.slice(1) : rawSearch;
  if (raw.length === 0) return "";
  const kept: string[] = [];
  for (const part of raw.split("&")) {
    if (part.length === 0) continue;
    const equals = part.indexOf("=");
    const rawName = equals === -1 ? part : part.slice(0, equals);
    if (!isSensitiveParamName(decodeParamName(rawName), options)) {
      kept.push(part);
    }
  }
  return kept.join("&");
}

function optionSet(values: Iterable<string> | undefined): Set<string> {
  const result = new Set<string>();
  if (values === undefined) return result;
  for (const value of values) result.add(normalizeName(value));
  return result;
}

/**
 * Returns true when a parameter name is token-like (or, by default, PII-like).
 * Name matching is case-insensitive and accepts snake_case, camelCase and
 * kebab-case spellings.
 */
export function isSensitiveParamName(
  name: string,
  options: SensitiveParamOptions = {},
): boolean {
  const normalized = normalizeName(name);
  if (normalized.length === 0) return false;

  const allowed = optionSet(options.allowNames);
  if (allowed.has(normalized)) return false;

  const extra = optionSet(options.extraNames);
  if (extra.has(normalized) || hasSegment(normalized, extra)) return true;

  if (
    SENSITIVE_EXACT_NAMES.has(normalized) ||
    hasSegment(normalized, SENSITIVE_SEGMENTS)
  ) {
    return true;
  }

  if (options.includePii !== false && hasSegment(normalized, PII_SEGMENTS)) {
    return true;
  }

  return false;
}

/** Alias used by callers that prefer the "token" terminology. */
export const isTokenParam = isSensitiveParamName;

interface ParsedInput {
  url: URL;
  kind: "absolute" | "protocol-relative" | "relative";
  hadLeadingSlash: boolean;
  hadExplicitPath?: boolean;
}

function parseInput(input: string | URL): ParsedInput | null {
  if (input instanceof URL) {
    return { url: new URL(input.href), kind: "absolute", hadLeadingSlash: true };
  }

  const raw = String(input).trim();
  if (raw.length === 0) return null;

  if (/^\/\//.test(raw)) {
    try {
      return {
        url: new URL(`https:${raw}`),
        kind: "protocol-relative",
        hadLeadingSlash: true,
        hadExplicitPath: raw.replace(/^\/\/[^/?#]*/, "").startsWith("/"),
      };
    } catch {
      return null;
    }
  }

  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(raw)) {
    try {
      return {
        url: new URL(raw),
        kind: "absolute",
        hadLeadingSlash: true,
        hadExplicitPath: raw
          .replace(/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\/[^/?#]*/, "")
          .startsWith("/"),
      };
    } catch {
      return null;
    }
  }

  try {
    return {
      url: new URL(raw, "https://portal.invalid"),
      kind: "relative",
      hadLeadingSlash: raw.startsWith("/"),
    };
  } catch {
    return null;
  }
}

function serializeInput(parsed: ParsedInput): string {
  if (parsed.kind === "absolute") {
    if (parsed.url.pathname === "/" && parsed.hadExplicitPath === false) {
      return `${parsed.url.protocol}//${parsed.url.host}${parsed.url.search}${parsed.url.hash}`;
    }
    return parsed.url.href;
  }
  if (parsed.kind === "protocol-relative") {
    if (parsed.url.pathname === "/" && parsed.hadExplicitPath === false) {
      return `//${parsed.url.host}${parsed.url.search}${parsed.url.hash}`;
    }
    return `//${parsed.url.host}${parsed.url.pathname}${parsed.url.search}${parsed.url.hash}`;
  }
  const pathname = parsed.hadLeadingSlash
    ? parsed.url.pathname
    : parsed.url.pathname.replace(/^\//, "");
  return `${pathname}${parsed.url.search}${parsed.url.hash}`;
}

function stripFragment(
  hash: string,
  options: SensitiveParamOptions,
): string {
  if (options.keepHash) return hash;
  if (hash.length <= 1) return "";

  const body = hash.slice(1);
  if (body.startsWith("/")) {
    const queryIndex = body.indexOf("?");
    if (queryIndex === -1) return hash;
    const prefix = body.slice(0, queryIndex);
    const query = body.slice(queryIndex + 1);
    const serialized = filterRawQuery(query, options);
    return serialized.length > 0 ? `#${prefix}?${serialized}` : `#${prefix}`;
  }

  if (body.includes("=") || body.includes("&")) {
    const serialized = filterRawQuery(body, options);
    return serialized.length > 0 ? `#${serialized}` : "";
  }

  if (options.stripOpaqueHashTokens !== false) {
    if (JWT_LIKE.test(body) || OPAQUE_HASH_TOKEN.test(body)) return "";
  }

  return hash;
}

/**
 * Removes token-like parameters from both the query string and the fragment.
 * The original URL shape (absolute, protocol-relative, or relative) is kept.
 */
export function stripSensitiveParams(
  input: string | URL,
  options: SensitiveParamOptions = {},
): string {
  const parsed = parseInput(input);
  if (parsed === null) return typeof input === "string" ? input : input.href;

  const url = parsed.url;
  url.search = filterRawQuery(url.search, options);

  if (options.stripCredentials) {
    url.username = "";
    url.password = "";
  }

  url.hash = stripFragment(url.hash, options);
  return serializeInput({ ...parsed, url });
}

/** Alias with a security-oriented name. */
export const scrubUrl = stripSensitiveParams;
export const sanitizeReferrerUrl = stripSensitiveParams;
export const removeSensitiveParams = stripSensitiveParams;
export const sanitizeUrl = stripSensitiveParams;
export const stripTokenParams = stripSensitiveParams;
export const removeTokenParams = stripSensitiveParams;

/** Removes token-like names from the query string while preserving the hash. */
export function stripSensitiveQuery(
  input: string | URL,
  options: SensitiveParamOptions = {},
): string {
  return stripSensitiveParams(input, {
    ...options,
    keepHash: options.keepHash ?? true,
  });
}

/** Removes token-like parameters from a URL intended to be shared. */
export function createShareableUrl(
  input: string | URL,
  options: ShareableUrlOptions = {},
): string {
  if (
    options.sensitiveParamBehavior === "throw" &&
    hasSensitiveParams(input, options)
  ) {
    throw new Error("Refusing to create a share URL containing sensitive parameters");
  }
  return stripSensitiveParams(input, {
    ...options,
    stripCredentials: options.stripCredentials !== false,
  });
}

/** Alias used by portals that call the helper "toShareableUrl". */
export const toShareableUrl = createShareableUrl;
export const sanitizeShareableUrl = createShareableUrl;

/**
 * Builds a shareable URL by appending only non-sensitive parameters.
 * Sensitive parameters supplied by the caller are dropped (or rejected when
 * `sensitiveParamBehavior` is `"throw"`).
 */
export function buildShareableUrl(
  base: string | URL,
  params?:
    | Record<string, unknown>
    | URLSearchParams
    | Iterable<readonly [string, string]>,
  options: ShareableUrlOptions = {},
): string {
  let result = createShareableUrl(base, options);
  const parsed = parseInput(result);
  if (parsed === null) return result;

  if (params !== undefined) {
    const entries: Array<[string, string]> = [];
    if (
      params instanceof URLSearchParams ||
      (typeof params === "object" &&
        params !== null &&
        Symbol.iterator in params &&
        !(params instanceof URLSearchParams))
    ) {
      for (const [name, value] of params as Iterable<readonly [string, string]>) {
        entries.push([String(name), String(value)]);
      }
    } else {
      for (const [name, value] of Object.entries(
        params as Record<string, unknown>,
      )) {
        if (value === undefined || value === null) continue;
        if (Array.isArray(value)) {
          for (const item of value) entries.push([name, String(item)]);
        } else {
          entries.push([name, String(value)]);
        }
      }
    }

    const added = new URLSearchParams();
    for (const [name, value] of entries) {
      if (isSensitiveParamName(name, options)) {
        if (options.sensitiveParamBehavior === "throw") {
          throw new Error(`Refusing to put sensitive parameter "${name}" in a share URL`);
        }
        continue;
      }
      added.append(name, value);
    }

    const extra = added.toString();
    if (extra.length > 0) {
      const existing = parsed.url.search;
      parsed.url.search = existing.length > 0
        ? `${existing}&${extra}`
        : `?${extra}`;
    }
  }

  result = serializeInput(parsed);
  return result;
}

export const buildShareUrl = buildShareableUrl;
export const createShareUrl = createShareableUrl;

/** Returns true if a URL contains a token-like query or fragment parameter. */
export function hasSensitiveParams(
  input: string | URL,
  options: SensitiveParamOptions = {},
): boolean {
  const parsed = parseInput(input);
  if (parsed === null) return false;

  if (hasSensitiveRawQuery(parsed.url.search, options)) return true;

  if (options.keepHash) return false;
  const body = parsed.url.hash.slice(1);
  if (body.length === 0) return false;

  if (body.startsWith("/")) {
    const queryIndex = body.indexOf("?");
    if (queryIndex === -1) return false;
    return hasSensitiveRawQuery(body.slice(queryIndex + 1), options);
  }

  if (body.includes("=") || body.includes("&")) {
    return hasSensitiveRawQuery(body, options);
  }

  if (options.stripOpaqueHashTokens !== false) {
    return JWT_LIKE.test(body) || OPAQUE_HASH_TOKEN.test(body);
  }
  return false;
}
