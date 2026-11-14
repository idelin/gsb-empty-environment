/**
 * Small origin allow-list helper used by redirects and the one-time relay.
 * Patterns accept exact origins, host names, `*` and `*.example.com` forms.
 */

function escapeRegExp(value: string): string {
  return value.replace(/[.+?^${}()|[\]\\]/g, "\\$&");
}

function patternToRegExp(pattern: string): RegExp {
  const escaped = escapeRegExp(pattern).replace(/\*/g, ".*");
  return new RegExp(`^${escaped}$`, "i");
}

/** Returns true when `origin` matches one of the allow-list patterns. */
export function isOriginAllowed(
  origin: string,
  patterns: readonly string[] | undefined,
): boolean {
  if (patterns === undefined) return true;
  if (patterns.length === 0) return false;

  let parsed: URL;
  try {
    parsed = new URL(origin);
  } catch {
    return false;
  }

  const normalizedOrigin = parsed.origin.toLowerCase();
  const normalizedHost = parsed.host.toLowerCase();
  const normalizedHostname = parsed.hostname.toLowerCase();

  for (const rawPattern of patterns) {
    const pattern = String(rawPattern).trim().replace(/\/+$/, "");
    if (pattern.length === 0) continue;
    if (pattern === "*") return true;

    if (/^https?:\/\//i.test(pattern) || pattern.startsWith("//")) {
      const normalizedPattern = pattern.startsWith("//")
        ? `https:${pattern}`
        : pattern;
      if (!normalizedPattern.includes("*")) {
        try {
          if (new URL(normalizedPattern).origin.toLowerCase() === normalizedOrigin) {
            return true;
          }
        } catch {
          // Fall through to wildcard matching.
        }
      }
      if (
        patternToRegExp(normalizedPattern).test(normalizedOrigin) ||
        patternToRegExp(normalizedPattern).test(normalizedHost)
      ) {
        return true;
      }
      continue;
    }

    if (patternToRegExp(pattern).test(normalizedHostname)) return true;
    if (patternToRegExp(pattern).test(normalizedHost)) return true;
  }

  return false;
}

/** Normalizes a URL to its origin, or null when the URL is invalid. */
export function getUrlOrigin(url: string | URL): string | null {
  try {
    return (url instanceof URL ? url : new URL(url)).origin;
  } catch {
    return null;
  }
}
