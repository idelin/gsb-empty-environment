import { HeaderBag, type HeaderSource } from "./headers.ts";

/** The transport-neutral part of an incoming FedCM request. */
export interface FedCMHttpRequest {
  method?: string;
  url?: string;
  headers?: HeaderSource | null;
  /**
   * Set by the adapter when an upstream proxy followed a redirect before the
   * handler saw the request. Incoming `Request` objects do not expose this
   * information themselves.
   */
  redirected?: boolean;
  /** Alternative representation of `redirected` for proxies that count hops. */
  redirectCount?: number;
}

export interface RedirectOptions {
  redirected?: boolean;
  redirectCount?: number;
}

export function requestHeaders(request: FedCMHttpRequest): HeaderBag {
  return new HeaderBag(request.headers);
}

export function wasRedirected(
  request: FedCMHttpRequest,
  options?: RedirectOptions,
): boolean {
  return (
    request.redirected === true ||
    (typeof request.redirectCount === "number" && request.redirectCount > 0) ||
    options?.redirected === true ||
    (typeof options?.redirectCount === "number" && options.redirectCount > 0)
  );
}

/**
 * Parses an Origin header or a registered origin. Only an absolute http(s)
 * origin is accepted; `null`, wildcards, credentials, paths, queries and
 * fragments are rejected. Default ports are normalized by the URL parser.
 */
export function parseOrigin(value: string): string | null {
  const trimmed = value.trim();
  if (!trimmed || trimmed === "null") return null;

  let url: URL;
  try {
    url = new URL(trimmed);
  } catch {
    return null;
  }

  if (url.protocol !== "https:" && url.protocol !== "http:") return null;
  if (url.username !== "" || url.password !== "") return null;
  if (url.pathname !== "/" || url.search !== "" || url.hash !== "") return null;

  return url.origin;
}

export function parseUrl(value?: string): URL | null {
  if (!value) return null;
  try {
    return new URL(value);
  } catch {
    return null;
  }
}
