import {
  hasSensitiveParams,
  type SensitiveParamOptions,
} from "./params.ts";

export interface RequestPathLike {
  url?: string | URL;
  originalUrl?: string;
  path?: string;
  pathname?: string;
  search?: string;
  href?: string;
}

export type PathMatcher = string | RegExp | ((pathname: string) => boolean);

export interface SensitivePathOptions extends SensitiveParamOptions {
  /** Additional path prefixes, globs, regular expressions or predicates. */
  extraSensitivePaths?: Iterable<PathMatcher>;
  /** Additional regular expressions used to classify a path as sensitive. */
  sensitivePathPatterns?: Iterable<RegExp>;
  /**
   * Treat any request whose query/fragment contains a token-like parameter as
   * sensitive. Defaults to true; this is a security floor even for otherwise
   * public-looking paths such as `/landing?token=...`.
   */
  treatTokenQueryAsSensitive?: boolean;
}

const DEFAULT_SENSITIVE_PATH =
  /(?:^|\/)(?:auth(?:entication|orize)?|login|log-in|signin|sign-in|signout|sign-out|logout|log-out|oauth2?|oidc|sso|saml|mfa|2fa|otp|reset|reset-password|password|passwd|forgot|forgot-password|recover|recovery|verify|verification|confirm|confirmation|activate|activation|unsubscribe|token|tokens|invite|invitation|magic|magic-link|email|emails|email-center|emailcenter|mail|mailcenter|webmail|mailbox|inbox|help|help-center|helpcenter|support|support-center|supportcenter|account|accounts|security|session|sessions|admin|settings|billing|payment|checkout|profile|redirect|redirects|relay|__referrer-relay|__referrer-redirect)(?=[-_.\/]|$)/i;

/** Extracts a request target from strings, URLs, fetch Requests or Node reqs. */
export function getRequestTarget(input: unknown): string {
  if (typeof input === "string") return input;
  if (input instanceof URL) return input.href;
  if (input === undefined || input === null) return "/";

  if (typeof input === "object") {
    const value = input as RequestPathLike;
    if (typeof value.originalUrl === "string") return value.originalUrl;
    if (typeof value.url === "string") return value.url;
    if (value.url instanceof URL) return value.url.href;
    if (typeof value.href === "string") return value.href;
    if (typeof value.pathname === "string") {
      return `${value.pathname}${value.search ?? ""}`;
    }
    if (typeof value.path === "string") {
      return `${value.path}${value.search ?? ""}`;
    }
  }

  return "/";
}

function safeDecode(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

/** Normalizes a request target to a pathname suitable for policy matching. */
export function normalizePath(input: unknown): string {
  const target = getRequestTarget(input);
  let path: string;

  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(target)) {
    try {
      path = new URL(target).pathname;
    } catch {
      path = target.split(/[?#]/, 1)[0];
    }
  } else {
    path = target.split(/[?#]/, 1)[0];
  }

  path = safeDecode(path.replace(/\\/g, "/")).normalize("NFKC");
  path = path.replace(/\/{2,}/g, "/");

  const segments: string[] = [];
  for (const segment of path.split("/")) {
    if (segment.length === 0 || segment === ".") continue;
    if (segment === "..") {
      segments.pop();
      continue;
    }
    segments.push(segment);
  }

  return `/${segments.join("/")}`;
}

function globToRegExp(pattern: string): RegExp {
  const escaped = pattern
    .replace(/[.+?^${}()|[\]\\]/g, "\\$&")
    .replace(/\*/g, ".*");
  return new RegExp(`^${escaped}(?:[/?#]|$)`, "i");
}

/** Matches one path matcher against an already-normalized pathname. */
export function matchesPathMatcher(
  pathname: string,
  matcher: PathMatcher,
): boolean {
  if (typeof matcher === "function") return matcher(pathname);

  if (matcher instanceof RegExp) {
    matcher.lastIndex = 0;
    const matched = matcher.test(pathname);
    matcher.lastIndex = 0;
    return matched;
  }

  const raw = String(matcher).trim();
  if (raw.length === 0) return false;
  if (raw === "*" || raw === "/*") return true;

  if (raw.includes("*")) return globToRegExp(raw).test(pathname);

  let prefix = raw;
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(prefix)) {
    try {
      prefix = new URL(prefix).pathname;
    } catch {
      // Keep the original string; it will almost certainly not match.
    }
  }
  prefix = normalizePath(prefix);
  const candidate = normalizePath(pathname);

  return (
    candidate === prefix ||
    (prefix !== "/" && candidate.startsWith(`${prefix}/`))
  );
}

/**
 * Classifies a path as sensitive. Email, help center, authentication and
 * account-management pages are sensitive by default. A token-like query
 * parameter makes any path sensitive.
 */
export function isSensitivePath(
  input: unknown,
  options: SensitivePathOptions = {},
): boolean {
  const target = getRequestTarget(input);

  if (
    options.treatTokenQueryAsSensitive !== false &&
    hasSensitiveParams(target, options)
  ) {
    return true;
  }

  const pathname = normalizePath(target);
  if (DEFAULT_SENSITIVE_PATH.test(pathname)) return true;

  if (options.extraSensitivePaths !== undefined) {
    for (const matcher of options.extraSensitivePaths) {
      if (matchesPathMatcher(pathname, matcher)) return true;
    }
  }

  if (options.sensitivePathPatterns !== undefined) {
    for (const pattern of options.sensitivePathPatterns) {
      pattern.lastIndex = 0;
      if (pattern.test(pathname)) return true;
      pattern.lastIndex = 0;
    }
  }

  return false;
}

/** Returns the pathname of a request target, without query or fragment. */
export function getPathname(input: unknown): string {
  return normalizePath(input);
}

/** Alias for callers that prefer "isRestrictedPath". */
export const isRestrictedPath = isSensitivePath;
