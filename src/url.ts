import { SubmitError } from "./types.ts";

/**
 * Parse an absolute http(s) origin such as `https://host.example:8443`.
 * Throws {@link SubmitError} rather than returning a partially parsed value.
 */
export function parseHttpOrigin(value: string): URL {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new SubmitError("INVALID_SNAPSHOT", `origin is not a valid URL: ${value}`);
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new SubmitError(
      "UNSAFE_ACTION_URL",
      `only http(s) origins are supported, got ${url.protocol}`,
    );
  }
  if (!url.hostname) {
    throw new SubmitError("INVALID_SNAPSHOT", `origin is missing a host: ${value}`);
  }
  return url;
}

/**
 * Resolve the form action against the *captured* base URL. The empty action
 * attribute follows HTML semantics and resolves to the base URL itself.
 *
 * Because the base is snapshotted at approval time, a later `<base>` change
 * cannot move an already approved target.
 */
export function resolveAction(baseUrl: string, action: string): URL {
  let base: URL;
  try {
    base = new URL(baseUrl);
  } catch {
    throw new SubmitError("UNSAFE_BASE_URL", `base URL is not valid: ${baseUrl}`);
  }
  if (base.protocol !== "http:" && base.protocol !== "https:") {
    throw new SubmitError("UNSAFE_BASE_URL", `base URL must be http(s): ${baseUrl}`);
  }

  try {
    return new URL(action === "" ? base.href : action, base);
  } catch {
    throw new SubmitError("UNSAFE_ACTION_URL", `action cannot be resolved: ${action}`);
  }
}

/** Origin tuple comparison per the HTML "same origin" algorithm. */
export function isSameOrigin(a: URL, b: URL): boolean {
  return a.protocol === b.protocol && a.hostname === b.hostname && a.port === b.port;
}

/** Default policy: forms may only submit back to their own origin. */
export function sameOriginPolicy(documentOrigin: string, target: URL): boolean {
  return isSameOrigin(parseHttpOrigin(documentOrigin), target);
}

/** Percent-encode form field names/values per the application/x-www-form rules. */
export function encodeFormComponent(value: string): string {
  return encodeURIComponent(value)
    .replace(/%20/g, "+")
    .replace(/[!'()*]/g, (ch) =>
      "%" + ch.charCodeAt(0).toString(16).toUpperCase().padStart(2, "0"),
    );
}

/** Replace the query component of a URL while preserving the rest verbatim. */
export function withQuery(url: URL, query: string): URL {
  const next = new URL(url.href);
  next.search = query;
  return next;
}
