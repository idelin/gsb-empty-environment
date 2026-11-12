/**
 * Origin parsing and comparison helpers.
 *
 * An origin is represented as its serialized tuple form
 * (`scheme://host:port`). Opaque origins are represented by the literal
 * null-origin sentinel and never compare equal to any other origin,
 * including another opaque origin.
 */

export const OPAQUE_ORIGIN = "null" as const;

export type Origin = string;

const DEFAULT_PORTS: Readonly<Record<string, number>> = {
  "http:": 80,
  "https:": 443,
  "ws:": 80,
  "wss:": 443,
  "ftp:": 21,
};

/**
 * Serialize the origin of `rawUrl`. Throws when the URL cannot be parsed or
 * uses a scheme that carries no trustworthy tuple origin.
 */
export function originOf(rawUrl: string): Origin {
  let url: URL;
  try {
    url = new URL(rawUrl);
  } catch {
    throw new TypeError(`Cannot derive origin from malformed URL: ${rawUrl}`);
  }

  const scheme = url.protocol;
  if (!DEFAULT_PORTS[scheme] && scheme !== "file:") {
    throw new TypeError(`URL scheme ${scheme} does not define an origin`);
  }
  if (scheme === "file:") {
    // File URLs produce opaque origins per the URL standard.
    return OPAQUE_ORIGIN;
  }

  const host = url.hostname;
  if (!host) {
    return OPAQUE_ORIGIN;
  }

  const port = url.port ? Number(url.port) : DEFAULT_PORTS[scheme];
  return `${scheme}//${host}:${port}`;
}

/**
 * Return true only when both inputs are serialized tuple origins that match
 * on scheme, host and port. Two opaque origins are never same-origin.
 */
export function sameOrigin(a: Origin, b: Origin): boolean {
  if (a === OPAQUE_ORIGIN || b === OPAQUE_ORIGIN) {
    return false;
  }
  return a === b;
}

/**
 * Normalize a URL into a cache key: parsed absolute URL with its fragment
 * removed. Throws on relative or malformed input.
 */
export function cacheKey(rawUrl: string): string {
  let url: URL;
  try {
    url = new URL(rawUrl);
  } catch {
    throw new TypeError(`Cannot use malformed URL as cache key: ${rawUrl}`);
  }
  url.hash = "";
  return url.href;
}
