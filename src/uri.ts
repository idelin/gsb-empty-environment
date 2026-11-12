import type { AcctIdentifier } from "./types.ts";

const HTTPS_BASE = "https://";
const DEFAULT_PORTS = new Map([
  ["http:", "80"],
  ["https:", "443"],
]);

export class InvalidAcctError extends TypeError {
  readonly code = "invalid_acct";

  constructor(message: string) {
    super(message);
    this.name = "InvalidAcctError";
  }
}

export class InvalidUrlError extends TypeError {
  readonly code = "invalid_url";

  constructor(message: string) {
    super(message);
    this.name = "InvalidUrlError";
  }
}

function normalizeHostname(hostname: string): string {
  return hostname.toLowerCase().replace(/\.+$/, "");
}

function normalizeAuthority(hostname: string, port: string, protocol = "https:"): string {
  const normalizedHostname = normalizeHostname(hostname);
  const defaultPort = DEFAULT_PORTS.get(protocol);
  if (!port || port === defaultPort) {
    return normalizedHostname;
  }
  return `${normalizedHostname}:${port}`;
}

/**
 * Parse a bare authority (host[:port]) without allowing paths, credentials,
 * queries, or fragments to smuggle a second host into the lookup.
 */
function parseAuthority(authority: string): { host: string; hostname: string; port: string } {
  if (/[/?#\\]/.test(authority)) {
    throw new InvalidUrlError(`Invalid host: ${authority}`);
  }

  let parsed: URL;
  try {
    parsed = new URL(`${HTTPS_BASE}${authority}`);
  } catch {
    throw new InvalidUrlError(`Invalid host: ${authority}`);
  }

  if (parsed.protocol !== "https:" || parsed.username || parsed.password) {
    throw new InvalidUrlError(`Invalid host: ${authority}`);
  }
  if (parsed.pathname !== "/" || parsed.search || parsed.hash) {
    throw new InvalidUrlError(`Invalid host: ${authority}`);
  }

  const hostname = normalizeHostname(parsed.hostname);
  if (!hostname) {
    throw new InvalidUrlError("Host is empty");
  }

  return {
    hostname,
    port: parsed.port,
    host: normalizeAuthority(hostname, parsed.port, parsed.protocol),
  };
}

export function tryParseAcct(raw: string): AcctIdentifier | null {
  if (typeof raw !== "string" || raw.length === 0) {
    return null;
  }
  if (raw.trim() !== raw || /[\u0000-\u0020\u007f]/.test(raw)) {
    return null;
  }

  const scheme = raw.slice(0, 5);
  if (scheme.toLowerCase() !== "acct:") {
    return null;
  }

  const subject = raw.slice(5);
  const at = subject.lastIndexOf("@");
  if (at <= 0 || at === subject.length - 1) {
    return null;
  }

  const user = subject.slice(0, at);
  const authority = subject.slice(at + 1);
  if (user.length === 0 || authority.length === 0) {
    return null;
  }

  try {
    const parsedAuthority = parseAuthority(authority);
    return {
      uri: raw,
      user,
      host: parsedAuthority.host,
      hostname: parsedAuthority.hostname,
      port: parsedAuthority.port,
    };
  } catch {
    return null;
  }
}

export function parseAcct(raw: string): AcctIdentifier {
  const parsed = tryParseAcct(raw);
  if (!parsed) {
    throw new InvalidAcctError(`Not a valid acct URI: ${raw}`);
  }
  return parsed;
}

/**
 * Normalize a host or host:port value for identity comparisons. A full HTTPS
 * URL is also accepted, but any path, credentials, query, or fragment is
 * rejected.
 */
export function normalizeHost(value: string): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new InvalidUrlError("Host is empty");
  }

  let authority = value;
  if (value.includes("://")) {
    let parsed: URL;
    try {
      parsed = new URL(value);
    } catch {
      throw new InvalidUrlError(`Invalid host: ${value}`);
    }
    if (
      parsed.protocol !== "https:" ||
      parsed.username ||
      parsed.password ||
      parsed.pathname !== "/" ||
      parsed.search ||
      parsed.hash
    ) {
      throw new InvalidUrlError(`Invalid host: ${value}`);
    }
    authority = `${parsed.hostname}${parsed.port ? `:${parsed.port}` : ""}`;
  }

  return parseAuthority(authority).host;
}

export function tryNormalizeHost(value: string): string | null {
  try {
    return normalizeHost(value);
  } catch {
    return null;
  }
}

export function sameHost(left: string, right: string): boolean {
  return normalizeHost(left) === normalizeHost(right);
}

/**
 * Extract the normalized hostname (no port) from a host or host:port value.
 */
export function hostnameOf(host: string): string {
  const normalized = normalizeHost(host);
  return parseAuthority(normalized).hostname;
}

/**
 * Convert an arbitrary URL string to an HTTPS URL suitable for a request.
 * Throws on non-HTTPS, credentials, or unparsable values.
 */
export function toHttpsUrl(raw: string, base?: string): URL {
  let parsed: URL;
  try {
    parsed = new URL(raw, base);
  } catch {
    throw new InvalidUrlError(`Invalid URL: ${raw}`);
  }

  if (parsed.protocol !== "https:") {
    throw new InvalidUrlError(`Refusing non-HTTPS URL: ${parsed.href}`);
  }
  if (parsed.username || parsed.password) {
    throw new InvalidUrlError("Refusing URL with credentials");
  }
  return parsed;
}

export function tryToHttpsUrl(raw: string, base?: string): URL | null {
  try {
    return toHttpsUrl(raw, base);
  } catch {
    return null;
  }
}

export function hostOfUrl(url: URL): string {
  return normalizeAuthority(url.hostname, url.port, url.protocol);
}

export function encodeAcctForQuery(acctUri: string): string {
  return encodeURIComponent(acctUri);
}
