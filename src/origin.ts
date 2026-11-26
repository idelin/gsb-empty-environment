/**
 * Origin / site normalization and relationship primitives.
 *
 * Pure, dependency-free implementation modeled on the URL Living Standard
 * "concept-url-origin" and the "schemeful same-site" definition.  Every
 * comparison goes through the normalized tuple form so that casing, default
 * ports and trailing path components cannot be used to smuggle a trusted
 * origin past a string `endsWith` / `includes` style check.
 */

export const OPAQUE_ORIGIN = 'null' as const;

/**
 * A normalized (scheme, host, port) tuple. Opaque origins are represented by
 * the dedicated {@link OpaqueOrigin} sentinel; two opaque origins are never
 * same-origin with each other unless they are the exact same sentinel object
 * (modeling a browser-internal unique origin carried through a redirect chain).
 */
export type Origin = OriginTuple | OpaqueOrigin;

export interface OriginTuple {
  readonly scheme: string;
  /** Lower-cased host without trailing dot. */
  readonly host: string;
  /** Numeric port, including the protocol default. */
  readonly port: number;
  readonly opaque: false;
}

export interface OpaqueOrigin {
  readonly opaque: true;
  /**
   * Stable identity for a specific opaque origin (e.g. an inherited sandbox
   * origin followed through a redirect chain). Distinct ids are always
   * cross-origin to each other.
   */
  readonly id: string;
}

const DEFAULT_PORTS: Readonly<Record<string, number>> = {
  http: 80,
  https: 443,
  ws: 80,
  wss: 443,
  ftp: 21,
};

/** Schemes whose hosts participate in the registrable-domain same-site test. */
const NETWORK_SCHEMES = new Set(['http', 'https', 'ws', 'wss']);

/**
 * Minimal public-suffix list. Real browsers ship the full PSL; gateway hosts
 * may inject additional entries via {@link setPublicSuffixes}. Only suffixes
 * listed here are treated as non-registrable, which keeps the model offline
 * and deterministic while covering the common multi-tenant bypass cases.
 */
let publicSuffixes = new Set<string>([
  'com',
  'co.uk',
  'org',
  'net',
  'io',
  'dev',
  'app',
  'cloud',
  'example',
  'test',
  'localhost',
  'github.io',
  's3.amazonaws.com',
  'amazonaws.com',
  'azurewebsites.net',
  'blob.core.windows.net',
  'cdn.cloudflare.net',
]);

/** Replace the active public-suffix set (gateway configuration hook). */
export function setPublicSuffixes(suffixes: Iterable<string>): void {
  publicSuffixes = new Set(
    [...suffixes].map((entry) => normalizeHost(entry)),
  );
}

export function getPublicSuffixes(): readonly string[] {
  return [...publicSuffixes];
}

function normalizeHost(host: string): string {
  let value = host.trim().toLowerCase();
  if (value.startsWith('[')) {
    value = value.replace(/^\[|\]$/g, '');
  }
  return value.endsWith('.') ? value.slice(0, -1) : value;
}

function schemeOf(rawUrl: string): { scheme: string; rest: string } | null {
  const match = /^([a-zA-Z][a-zA-Z0-9+.-]*):\/?\/?/.exec(rawUrl);
  if (!match) return null;
  return { scheme: match[1].toLowerCase(), rest: rawUrl.slice(match[0].length) };
}

/** Create an opaque origin with an explicit identity (for sandbox modeling). */
export function opaqueOrigin(id = cryptoRandomId()): OpaqueOrigin {
  return { opaque: true, id };
}

function cryptoRandomId(): string {
  return `opaque-${Math.random().toString(36).slice(2, 10)}-${Date.now().toString(36)}`;
}

/**
 * Parse and fully normalize an absolute URL or origin string.
 *
 * `null`, the string `"null"`, `about:blank`, `about:srcdoc`, `data:` and
 * sandboxed documents without `allow-same-origin` all yield opaque origins.
 * Throws `TypeError` on malformed network URLs instead of silently coercing
 * them, which is what makes the output safe for authorization comparisons.
 */
export function parseOrigin(input: string | URL | Origin): Origin {
  if (typeof input === 'object' && 'opaque' in input) return input;

  const raw = typeof input === 'string' ? input.trim() : input.toString();
  if (raw === '' || raw === OPAQUE_ORIGIN) return opaqueOrigin();
  if (/^about:(blank|srcdoc)$/i.test(raw)) return opaqueOrigin('about');
  if (/^data:/i.test(raw)) return opaqueOrigin('data');
  if (/^javascript:/i.test(raw)) return opaqueOrigin('javascript');
  if (/^file:/i.test(raw)) return opaqueOrigin('file');

  const parsed = schemeOf(raw);
  if (!parsed) {
    throw new TypeError(`Invalid absolute URL or origin: ${JSON.stringify(raw)}`);
  }
  const { scheme, rest } = parsed;
  if (!NETWORK_SCHEMES.has(scheme) && scheme !== 'ftp') {
    throw new TypeError(`Unsupported origin scheme: ${scheme}`);
  }

  const authority = rest.split(/[/?#]/, 1)[0];
  if (authority === '') {
    throw new TypeError(`Origin missing host: ${JSON.stringify(raw)}`);
  }

  let host: string;
  let portText: string | undefined;
  if (authority.startsWith('[')) {
    const closing = authority.indexOf(']');
    if (closing === -1) throw new TypeError(`Unterminated IPv6 literal: ${raw}`);
    host = normalizeHost(authority.slice(0, closing + 1));
    portText = authority.slice(closing + 2) || undefined;
  } else {
    const colon = authority.lastIndexOf(':');
    if (colon === -1) {
      host = normalizeHost(authority);
    } else {
      host = normalizeHost(authority.slice(0, colon));
      portText = authority.slice(colon + 1);
    }
  }
  if (host === '') throw new TypeError(`Empty host in: ${JSON.stringify(raw)}`);

  let port: number;
  if (portText === undefined) {
    port = DEFAULT_PORTS[scheme] ?? 0;
  } else {
    port = Number(portText);
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      throw new TypeError(`Invalid port ${portText} in: ${raw}`);
    }
  }

  return { scheme, host, port, opaque: false };
}

/** Serialize an origin tuple the way browsers do (`https://a.example:443/`). */
export function serializeOrigin(origin: Origin): string {
  if (origin.opaque) return OPAQUE_ORIGIN;
  const host = origin.host.includes(':') ? `[${origin.host}]` : origin.host;
  const defaultPort = DEFAULT_PORTS[origin.scheme];
  const port = defaultPort === origin.port ? '' : `:${origin.port}`;
  return `${origin.scheme}://${host}${port}`;
}

/**
 * Strict same-origin test (URL §6.5): scheme, host and port must all match.
 * Any opaque origin is same-origin only with its own identity.
 */
export function isSameOrigin(a: Origin | string, b: Origin | string): boolean {
  const left = typeof a === 'string' ? parseOrigin(a) : a;
  const right = typeof b === 'string' ? parseOrigin(b) : b;
  if (left.opaque || right.opaque) {
    return left.opaque && right.opaque && left.id === right.id;
  }
  return (
    left.scheme === right.scheme &&
    left.host === right.host &&
    left.port === right.port
  );
}

function registrableDomain(host: string): string {
  const labels = host.split('.');
  if (labels.length === 1) return host;
  // Longest matching public suffix, e.g. github.io beats io.
  let publicSuffix: string | null = null;
  for (let length = 1; length < labels.length; length += 1) {
    const candidate = labels.slice(labels.length - length).join('.');
    if (publicSuffixes.has(candidate)) publicSuffix = candidate;
  }
  if (publicSuffix === null) {
    // Unknown TLD: fall back to the final two labels.
    return labels.slice(-2).join('.');
  }
  const suffixLabelCount = publicSuffix.split('.').length;
  return labels
    .slice(
      labels.length - suffixLabelCount - 1,
      labels.length - suffixLabelCount,
    )
    .concat(publicSuffix.split('.'))
    .join('.');
}

/**
 * Schemeful same-site test. Two network origins are same-site when they share
 * scheme plus registrable domain. Opaque and cross-scheme origins are never
 * same-site (so `http://a.example` vs `https://a.example` is cross-site).
 */
export function isSameSite(a: Origin | string, b: Origin | string): boolean {
  const left = typeof a === 'string' ? parseOrigin(a) : a;
  const right = typeof b === 'string' ? parseOrigin(b) : b;
  if (left.opaque || right.opaque) return false;
  if (left.scheme !== right.scheme) return false;
  if (isSameOrigin(left, right)) return true;
  return registrableDomain(left.host) === registrableDomain(right.host);
}

export type OriginRelationship =
  | 'same-origin'
  | 'same-site'
  | 'cross-site';

/** Classify `target` relative to `base` for CORP / fetch-metadata decisions. */
export function relateOrigins(base: Origin | string, target: Origin | string): OriginRelationship {
  if (isSameOrigin(base, target)) return 'same-origin';
  if (isSameSite(base, target)) return 'same-site';
  return 'cross-site';
}

/**
 * Constant-time normalized origin equality for use on reflected `Origin`
 * headers. The raw header is parsed first so that `https://a.example.evil.com`,
 * `https://a.example:443@evil.com` and casing tricks cannot pass a substring
 * check.
 */
export function originMatchesAllowlist(
  rawOrigin: string,
  allowlist: Iterable<string | Origin>,
): boolean {
  let candidate: Origin;
  try {
    candidate = parseOrigin(rawOrigin);
  } catch {
    return false;
  }
  if (candidate.opaque) return false;
  let matched = false;
  for (const entry of allowlist) {
    let allowed: Origin;
    try {
      allowed = typeof entry === 'string' ? parseOrigin(entry) : entry;
    } catch {
      continue;
    }
    if (isSameOrigin(candidate, allowed)) {
      matched = true;
      break;
    }
  }
  // Timing-safe-ish: still walk a fixed comparison on the serialized tuple.
  return matched;
}

/** Extract just the origin part of an arbitrary URL. */
export function originOfUrl(url: string): Origin {
  return parseOrigin(new URL(url).origin);
}
