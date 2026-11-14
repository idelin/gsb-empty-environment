import { domainToASCII } from "node:url";

export type RedirectUriErrorCode =
  | "invalid_redirect_uri"
  | "invalid_scheme"
  | "userinfo_not_allowed"
  | "fragment_not_allowed"
  | "port_not_allowed"
  | "path_traversal"
  | "loopback_not_allowed"
  | "custom_scheme_not_allowed";

export class RedirectUriError extends Error {
  readonly code: RedirectUriErrorCode;

  constructor(code: RedirectUriErrorCode, message: string) {
    super(message);
    this.name = "RedirectUriError";
    this.code = code;
  }
}

export interface RedirectUriPolicy {
  /** Loopback redirect URIs are only accepted when this is explicitly true. */
  allowLoopback?: boolean;
  /** Exact custom schemes that may be used, for example "com.example.app". */
  allowCustomSchemes?: readonly string[];
  /** Convenience alias: true allows any custom scheme, or pass an allowlist. */
  allowCustomScheme?: boolean | readonly string[];
}

export interface ParsedRedirectUri {
  /** Canonical URL serialization (IDNA host, lower-case scheme/host). */
  readonly uri: string;
  readonly scheme: string;
  readonly host: string;
  /** Effective port, including the scheme default port. */
  readonly port: number | null;
  readonly path: string;
  readonly pathSegments: readonly string[];
  /** Query component including the leading "?", or an empty string. */
  readonly query: string;
  readonly hasAuthority: boolean;
  readonly loopback: boolean;
  readonly customScheme: boolean;
  readonly hasWildcard: boolean;
  readonly wildcardSegments: readonly number[];
}

export interface RedirectUriValidationResult {
  readonly ok: boolean;
  /** The registered (or wildcard-expanded) URI when ok is true. */
  readonly redirectUri?: string;
  readonly error?: RedirectUriError;
}

const DEFAULT_PORTS: Readonly<Record<string, number>> = {
  http: 80,
  https: 443,
};
const FORBIDDEN_CUSTOM_SCHEMES = new Set([
  "data",
  "file",
  "javascript",
  "vbscript",
]);

const SCHEME_PATTERN = /^[A-Za-z][A-Za-z0-9+.-]*$/;
const BAD_WHITESPACE_OR_CONTROL = /[\u0000-\u0020\u007f-\u009f]/;
const MALFORMED_PERCENT_ESCAPE = /%(?![0-9A-Fa-f]{2})/;

function fail(code: RedirectUriErrorCode, message: string): never {
  throw new RedirectUriError(code, message);
}

function normalizeHostname(rawHostname: string): string {
  if (rawHostname.length === 0) {
    return "";
  }
  if (rawHostname.startsWith("[") && rawHostname.endsWith("]")) {
    return rawHostname.toLowerCase();
  }

  let hostname = rawHostname;
  if (hostname.endsWith(".")) {
    hostname = hostname.slice(0, -1);
  }
  if (hostname.length === 0) {
    fail("invalid_redirect_uri", "The redirect URI host is empty.");
  }

  const ascii = domainToASCII(hostname);
  if (!ascii) {
    fail("invalid_redirect_uri", "The redirect URI host is not a valid IDNA hostname.");
  }

  const normalized = ascii.toLowerCase().replace(/\.$/, "");
  if (
    normalized.length === 0 ||
    normalized.startsWith(".") ||
    normalized.endsWith(".") ||
    normalized.includes("..") ||
    normalized.includes("*")
  ) {
    fail("invalid_redirect_uri", "The redirect URI hostname is malformed.");
  }

  return normalized;
}

function isIPv4Literal(hostname: string): boolean {
  const parts = hostname.split(".");
  return (
    parts.length === 4 &&
    parts.every((part) => {
      if (!/^\d{1,3}$/.test(part)) {
        return false;
      }
      const value = Number(part);
      return value >= 0 && value <= 255;
    })
  );
}

function isLoopbackHostname(hostname: string): boolean {
  if (hostname === "localhost" || hostname.endsWith(".localhost")) {
    return true;
  }

  if (hostname.startsWith("[") && hostname.endsWith("]")) {
    const ipv6 = hostname.slice(1, -1).toLowerCase();
    if (ipv6 === "::1") {
      return true;
    }
    // IPv4-mapped IPv6 addresses in the 127.0.0.0/8 range.
    const mapped = /^::ffff:([0-9a-f]{1,4}):([0-9a-f]{1,4})$/.exec(ipv6);
    if (mapped) {
      const high = Number.parseInt(mapped[1], 16);
      if ((high >> 8) === 0x7f) {
        return true;
      }
    }
    return false;
  }

  if (isIPv4Literal(hostname)) {
    return Number(hostname.split(".")[0]) === 127;
  }

  return false;
}

function authorityEnd(value: string): number {
  for (let index = 0; index < value.length; index += 1) {
    const character = value[index];
    if (character === "/" || character === "?" || character === "#") {
      return index;
    }
  }
  return value.length;
}

function decodeForTraversalCheck(value: string): string {
  let decoded = value;
  for (let round = 0; round < 8; round += 1) {
    if (!decoded.includes("%")) {
      break;
    }
    if (MALFORMED_PERCENT_ESCAPE.test(decoded)) {
      // A literal "%" introduced by decoding "%25" is data, not a new escape.
      if (round > 0) {
        break;
      }
      fail("invalid_redirect_uri", "The redirect URI contains an invalid percent escape.");
    }
    try {
      const next = decodeURIComponent(decoded);
      if (next === decoded) {
        break;
      }
      decoded = next;
    } catch {
      fail("invalid_redirect_uri", "The redirect URI contains invalid UTF-8 percent escapes.");
    }
  }
  return decoded;
}

function validateRawPath(rawPath: string): void {
  if (rawPath.length === 0) {
    return;
  }
  if (MALFORMED_PERCENT_ESCAPE.test(rawPath)) {
    fail("invalid_redirect_uri", "The redirect URI path contains an invalid percent escape.");
  }

  for (const rawSegment of rawPath.split("/")) {
    const decoded = decodeForTraversalCheck(rawSegment);
    if (decoded === "." || decoded === "..") {
      fail("path_traversal", "Dot segments are not allowed in a redirect URI path.");
    }
    if (decoded.includes("/") || decoded.includes("\\")) {
      fail(
        "path_traversal",
        "Encoded path separators are not allowed in a redirect URI path.",
      );
    }
    if (/[\u0000-\u001f\u007f]/.test(decoded)) {
      fail("invalid_redirect_uri", "Control characters are not allowed in a redirect URI path.");
    }
  }
}

function validateRawQuery(rawQuery: string): void {
  if (rawQuery.length === 0) {
    return;
  }
  if (MALFORMED_PERCENT_ESCAPE.test(rawQuery)) {
    fail("invalid_redirect_uri", "The redirect URI query contains an invalid percent escape.");
  }
  const decoded = decodeForTraversalCheck(rawQuery);
  if (/[\u0000-\u001f\u007f]/.test(decoded)) {
    fail("invalid_redirect_uri", "Control characters are not allowed in a redirect URI query.");
  }
}

function parseScheme(value: string): { scheme: string; rest: string } {
  const separator = value.indexOf(":");
  if (separator <= 0) {
    fail("invalid_redirect_uri", "The redirect URI must be an absolute URI with a scheme.");
  }

  const scheme = value.slice(0, separator);
  if (!SCHEME_PATTERN.test(scheme)) {
    fail("invalid_scheme", "The redirect URI scheme is invalid.");
  }

  return {
    scheme: scheme.toLowerCase(),
    rest: value.slice(separator + 1),
  };
}

function parseStructural(input: string): ParsedRedirectUri {
  if (typeof input !== "string") {
    fail("invalid_redirect_uri", "The redirect URI must be a string.");
  }
  if (input.length === 0 || input.length > 8192) {
    fail("invalid_redirect_uri", "The redirect URI has an invalid length.");
  }
  if (input !== input.trim() || BAD_WHITESPACE_OR_CONTROL.test(input)) {
    fail("invalid_redirect_uri", "Whitespace and control characters are not allowed.");
  }
  if (input.includes("\\")) {
    fail("invalid_redirect_uri", "Backslashes are not allowed in a redirect URI.");
  }
  if (input.includes("#")) {
    fail("fragment_not_allowed", "Redirect URIs must not contain a fragment.");
  }

  const { scheme, rest } = parseScheme(input);

  let hasAuthority = false;
  let rawAuthority = "";
  let pathAndQuery = rest;
  if (rest.startsWith("//")) {
    hasAuthority = true;
    const end = authorityEnd(rest.slice(2));
    rawAuthority = rest.slice(2, end + 2);
    pathAndQuery = rest.slice(end + 2);
    if (rawAuthority.includes("@")) {
      fail("userinfo_not_allowed", "Redirect URIs must not contain user information.");
    }
    if (
      rawAuthority.length === 0 &&
      (scheme === "http" || scheme === "https")
    ) {
      fail("invalid_redirect_uri", "HTTP redirect URIs must contain a non-empty authority.");
    }
  } else if (scheme === "http" || scheme === "https") {
    fail("invalid_redirect_uri", "HTTP redirect URIs must contain an authority.");
  }

  const queryIndex = pathAndQuery.indexOf("?");
  const rawPath = queryIndex === -1 ? pathAndQuery : pathAndQuery.slice(0, queryIndex);
  const rawQuery = queryIndex === -1 ? "" : pathAndQuery.slice(queryIndex + 1);
  validateRawPath(rawPath);
  validateRawQuery(rawQuery);

  let url: URL;
  try {
    url = new URL(input);
  } catch {
    fail("invalid_redirect_uri", "The redirect URI cannot be parsed.");
  }

  if (url.protocol !== `${scheme}:`) {
    fail("invalid_redirect_uri", "The redirect URI scheme is ambiguous.");
  }
  if (url.username.length > 0 || url.password.length > 0) {
    fail("userinfo_not_allowed", "Redirect URIs must not contain user information.");
  }
  if (url.hash.length > 0) {
    fail("fragment_not_allowed", "Redirect URIs must not contain a fragment.");
  }

  const host = normalizeHostname(url.hostname);
  if (host.length === 0 && (scheme === "http" || scheme === "https")) {
    fail("invalid_redirect_uri", "HTTP redirect URIs must contain a host.");
  }

  let port: number | null = null;
  if (url.port.length > 0) {
    port = Number(url.port);
  } else if (scheme in DEFAULT_PORTS) {
    port = DEFAULT_PORTS[scheme] ?? null;
  }

  const path = url.pathname;
  const pathSegments = path.split("/");
  const wildcardSegments: number[] = [];
  for (let index = 0; index < pathSegments.length; index += 1) {
    const segment = pathSegments[index];
    if (segment.includes("*")) {
      if (segment !== "*") {
        fail(
          "invalid_redirect_uri",
          "A wildcard must occupy an entire path segment.",
        );
      }
      wildcardSegments.push(index);
    }
  }

  return {
    uri: url.href,
    scheme,
    host,
    port,
    path,
    pathSegments,
    query: url.search,
    hasAuthority,
    loopback: isLoopbackHostname(host),
    customScheme: scheme !== "http" && scheme !== "https",
    hasWildcard: wildcardSegments.length > 0,
    wildcardSegments,
  };
}

function allowedCustomSchemes(policy: RedirectUriPolicy | undefined): Set<string> | "any" {
  const configured = new Set<string>();
  if (policy?.allowCustomScheme === true) {
    return "any";
  }
  if (Array.isArray(policy?.allowCustomScheme)) {
    for (const scheme of policy.allowCustomScheme) {
      configured.add(scheme.toLowerCase());
    }
  }
  for (const scheme of policy?.allowCustomSchemes ?? []) {
    configured.add(scheme.toLowerCase());
  }
  return configured;
}

function enforcePolicy(parsed: ParsedRedirectUri, policy: RedirectUriPolicy | undefined): void {
  if (parsed.loopback && policy?.allowLoopback !== true) {
    fail(
      "loopback_not_allowed",
      "Loopback redirect URIs require explicit allowLoopback authorization.",
    );
  }

  if (parsed.customScheme) {
    if (FORBIDDEN_CUSTOM_SCHEMES.has(parsed.scheme)) {
      fail("invalid_scheme", `Custom scheme "${parsed.scheme}" is not allowed.`);
    }
    const allowed = allowedCustomSchemes(policy);
    if (allowed !== "any" && !allowed.has(parsed.scheme)) {
      fail(
        "custom_scheme_not_allowed",
        `Custom scheme "${parsed.scheme}" is not explicitly authorized.`,
      );
    }
    return;
  }

  if (parsed.scheme !== "https" && !(parsed.scheme === "http" && parsed.loopback)) {
    fail("invalid_scheme", "Non-loopback redirect URIs must use HTTPS.");
  }
}

/**
 * Parse and canonically normalize a redirect URI. Policy checks are applied by
 * default; use this for both registration and request-time validation.
 */
export function parseRedirectUri(
  input: string,
  policy?: RedirectUriPolicy,
): ParsedRedirectUri {
  const parsed = parseStructural(input);
  enforcePolicy(parsed, policy);
  return parsed;
}

/** Parse structure without applying loopback/custom-scheme registration policy. */
export function inspectRedirectUri(input: string): ParsedRedirectUri {
  return parseStructural(input);
}

export function normalizeRedirectUri(input: string, policy?: RedirectUriPolicy): string {
  return parseRedirectUri(input, policy).uri;
}

function pathsMatch(registered: ParsedRedirectUri, requested: ParsedRedirectUri): boolean {
  if (registered.pathSegments.length !== requested.pathSegments.length) {
    return false;
  }

  for (let index = 0; index < registered.pathSegments.length; index += 1) {
    const registeredSegment = registered.pathSegments[index];
    const requestedSegment = requested.pathSegments[index];
    if (registeredSegment === "*") {
      if (
        requestedSegment.length === 0 ||
        requestedSegment === "." ||
        requestedSegment === ".."
      ) {
        return false;
      }
      continue;
    }
    if (registeredSegment !== requestedSegment) {
      return false;
    }
  }

  return true;
}

function redirectUrisMatch(registered: ParsedRedirectUri, requested: ParsedRedirectUri): boolean {
  return (
    registered.scheme === requested.scheme &&
    registered.host === requested.host &&
    registered.port === requested.port &&
    registered.query === requested.query &&
    pathsMatch(registered, requested)
  );
}

/**
 * Match a requested redirect URI against one registered URI or template.
 * Returns the canonical registered URI, or the wildcard-expanded requested URI
 * when the registration contains an explicitly declared "*" path segment.
 */
export function matchRedirectUri(
  registeredUri: string,
  requestedUri: string,
  policy?: RedirectUriPolicy,
): string | null {
  let registered: ParsedRedirectUri;
  let requested: ParsedRedirectUri;
  try {
    registered = parseRedirectUri(registeredUri, policy);
    requested = inspectRedirectUri(requestedUri);
  } catch {
    return null;
  }

  if (!redirectUrisMatch(registered, requested)) {
    return null;
  }

  return registered.hasWildcard ? requested.uri : registered.uri;
}

export function isRedirectUriAllowed(
  registeredUri: string,
  requestedUri: string,
  policy?: RedirectUriPolicy,
): boolean {
  return matchRedirectUri(registeredUri, requestedUri, policy) !== null;
}

export function validateRedirectUri(
  registeredUri: string,
  requestedUri: string,
  policy?: RedirectUriPolicy,
): boolean {
  return isRedirectUriAllowed(registeredUri, requestedUri, policy);
}

export function assertRedirectUri(
  registeredUri: string,
  requestedUri: string,
  policy?: RedirectUriPolicy,
): string {
  const matched = matchRedirectUri(registeredUri, requestedUri, policy);
  if (matched === null) {
    fail("invalid_redirect_uri", "The redirect URI does not match the registered URI.");
  }
  return matched;
}

export function checkRedirectUri(
  registeredUri: string,
  requestedUri: string,
  policy?: RedirectUriPolicy,
): RedirectUriValidationResult {
  try {
    return {
      ok: true,
      redirectUri: assertRedirectUri(registeredUri, requestedUri, policy),
    };
  } catch (error) {
    if (error instanceof RedirectUriError) {
      return { ok: false, error };
    }
    throw error;
  }
}
