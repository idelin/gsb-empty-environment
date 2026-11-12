/**
 * Source-origin policy for the native bridge.
 *
 * An allow-listed entry may be:
 *  - a full origin, e.g. "https://app.example.com"
 *  - a bare host,    e.g. "app.example.com"
 *  - a wildcard host, e.g. "*.example.com" (one or more sub-domain labels)
 *
 * "*" matches exactly one or more labels, so "*.example.com" matches
 * "a.example.com" and "a.b.example.com" but never:
 *  - "example.com"          (no sub-domain label)
 *  - "evilexample.com"      (different label, a suffix is not enough)
 *  - "example.com.evil.net" (the allowed suffix is not the host tail)
 */

export type OriginPattern = string;

export interface ParsedPattern {
  readonly host: string;
  readonly wildcard: boolean;
}

function normalizeHost(host: string): string {
  // URL.hostname is lower-cased and punycode-encoded; strip a single
  // trailing dot (FQDN form), which names the same host.
  const trimmed = host.trim();
  return trimmed.endsWith('.') ? trimmed.slice(0, -1) : trimmed;
}

/** Parse an allow-listed pattern once, at construction time. */
export function parsePattern(pattern: OriginPattern): ParsedPattern {
  if (typeof pattern !== 'string' || pattern.length === 0) {
    throw new Error('allowNavigation entries must be non-empty strings');
  }

  let raw = pattern.trim();
  if (!/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(raw)) {
    raw = `https://${raw}`;
  }

  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    throw new Error(`invalid allowNavigation entry: ${pattern}`);
  }

  if (url.username !== '' || url.password !== '') {
    throw new Error(`allowNavigation entries must not contain credentials: ${pattern}`);
  }
  if (url.search !== '' || url.hash !== '') {
    throw new Error(`allowNavigation entries must not contain query/hash: ${pattern}`);
  }
  if (url.pathname !== '' && url.pathname !== '/') {
    throw new Error(`allowNavigation entries are origin/host only: ${pattern}`);
  }

  const host = normalizeHost(url.hostname);
  if (host === '') {
    throw new Error(`allowNavigation entry has no host: ${pattern}`);
  }
  const wildcard = host.startsWith('*.');
  if (host.includes('*') && !wildcard) {
    throw new Error(`wildcard is only allowed as the left-most label: ${pattern}`);
  }
  if (wildcard && host.length <= 2) {
    throw new Error(`wildcard pattern needs a domain after the "*": ${pattern}`);
  }

  return { host, wildcard };
}

/** Extract a normalized host from a message origin string. */
export function originToHost(origin: string): string | null {
  if (typeof origin !== 'string' || origin.length === 0) {
    return null;
  }
  let url: URL;
  try {
    url = new URL(origin);
  } catch {
    return null;
  }
  if (url.origin === 'null' || url.hostname === '') {
    return null;
  }
  return normalizeHost(url.hostname);
}

export function hostMatchesPattern(host: string, pattern: ParsedPattern): boolean {
  if (!pattern.wildcard) {
    return host === pattern.host;
  }
  const suffix = pattern.host.slice(1); // ".example.com"
  if (!host.endsWith(suffix)) {
    return false;
  }
  const prefix = host.slice(0, host.length - suffix.length);
  // At least one non-empty label made of hostname characters. Hosts such as
  // "example.com.evil.net" fail the suffix check already; this additionally
  // rejects empty labels.
  return prefix.length > 0 && /^[a-z0-9-]+(\.[a-z0-9-]+)*$/.test(prefix);
}

export function isAllowedOrigin(
  origin: string,
  patterns: readonly ParsedPattern[],
): boolean {
  const host = originToHost(origin);
  if (host === null) {
    return false;
  }
  return patterns.some((pattern) => hostMatchesPattern(host, pattern));
}
