export interface ParsedHstsPolicy {
  readonly maxAge: number;
  readonly includeSubDomains: boolean;
}

export interface HstsEntry extends ParsedHstsPolicy {
  readonly hostname: string;
  readonly expiresAt: number;
}

export interface HstsStoreOptions {
  readonly now?: () => number;
}

type HeaderValue = string | readonly string[] | null | undefined;

export type HstsHeaders =
  | ReadonlyMap<string, HeaderValue>
  | { get(name: string): HeaderValue }
  | readonly (readonly [string, HeaderValue])[]
  | Readonly<Record<string, HeaderValue>>;

const HSTS_HEADER_NAME = "strict-transport-security";
const TOKEN = /^[!#$%&'*+\-.^_`|~0-9a-z]+$/;
const DECIMAL_INTEGER = /^[0-9]+$/;
const IPV4_LITERAL = /^\d{1,3}(?:\.\d{1,3}){3}$/;
const MAX_SAFE_AGE = BigInt(Number.MAX_SAFE_INTEGER);

function headerValueToString(value: HeaderValue): string | null {
  if (typeof value === "string") {
    return value;
  }

  if (Array.isArray(value)) {
    return value.join(", ");
  }

  return null;
}

/**
 * Reads one header field case-insensitively. Multiple physical header lines
 * are combined with commas, which lets duplicate directives invalidate the
 * entire policy.
 */
export function readHstsHeader(headers: HstsHeaders | null | undefined): string | null {
  if (headers == null) {
    return null;
  }

  if (Array.isArray(headers)) {
    const values: string[] = [];

    for (const [name, value] of headers) {
      if (name.toLowerCase() !== HSTS_HEADER_NAME) {
        continue;
      }

      const text = headerValueToString(value);
      if (text != null) {
        values.push(text);
      }
    }

    return values.length > 0 ? values.join(", ") : null;
  }

  if (typeof headers === "object" && "get" in headers && typeof headers.get === "function") {
    const value = headerValueToString(headers.get(HSTS_HEADER_NAME));
    if (value != null) {
      return value;
    }
  }

  if (
    typeof headers === "object" &&
    Symbol.iterator in headers &&
    typeof headers[Symbol.iterator] === "function"
  ) {
    const values: string[] = [];

    for (const [name, value] of headers as Iterable<readonly [string, HeaderValue]>) {
      if (name.toLowerCase() !== HSTS_HEADER_NAME) {
        continue;
      }

      const text = headerValueToString(value);
      if (text != null) {
        values.push(text);
      }
    }

    if (values.length > 0) {
      return values.join(", ");
    }
  }

  if (typeof headers !== "object") {
    return null;
  }

  const values: string[] = [];
  for (const [name, value] of Object.entries(headers)) {
    if (name.toLowerCase() !== HSTS_HEADER_NAME) {
      continue;
    }

    const text = headerValueToString(value);
    if (text != null) {
      values.push(text);
    }
  }

  return values.length > 0 ? values.join(", ") : null;
}

/**
 * Parses a Strict-Transport-Security field value.
 *
 * A duplicated directive invalidates the whole field. Unknown directives are
 * ignored, but only after duplicate detection so duplicate unknown directives
 * are rejected too.
 */
export function parseHstsHeader(value: string): ParsedHstsPolicy | null {
  if (typeof value !== "string" || value.trim() === "") {
    return null;
  }

  let maxAge: bigint | null = null;
  let includeSubDomains = false;
  const seenDirectives = new Set<string>();

  for (const field of value.split(",")) {
    const directive = field.trim();
    if (directive === "") {
      return null;
    }

    const equalsIndex = directive.indexOf("=");
    const name = (equalsIndex === -1 ? directive : directive.slice(0, equalsIndex))
      .trim()
      .toLowerCase();
    const rawValue = equalsIndex === -1 ? null : directive.slice(equalsIndex + 1).trim();

    if (!TOKEN.test(name) || seenDirectives.has(name)) {
      return null;
    }
    seenDirectives.add(name);

    if (name === "max-age") {
      if (rawValue === null || !DECIMAL_INTEGER.test(rawValue)) {
        return null;
      }

      maxAge = BigInt(rawValue);
      continue;
    }

    if (name === "includesubdomains") {
      if (rawValue !== null) {
        return null;
      }
      includeSubDomains = true;
    }
  }

  if (maxAge === null) {
    return null;
  }

  const boundedMaxAge = maxAge > MAX_SAFE_AGE ? MAX_SAFE_AGE : maxAge;
  return {
    maxAge: Number(boundedMaxAge),
    includeSubDomains,
  };
}

function parseAbsoluteUrl(input: string | URL): URL | null {
  if (input instanceof URL) {
    return input;
  }

  if (typeof input !== "string") {
    return null;
  }

  try {
    return new URL(input);
  } catch {
    return null;
  }
}

function stripHostnameDecorations(hostname: string): string {
  let normalized = hostname.trim().toLowerCase();

  if (normalized.startsWith("[") && normalized.endsWith("]")) {
    normalized = normalized.slice(1, -1);
  }

  return normalized.endsWith(".")
    ? normalized.slice(0, -1)
    : normalized;
}

/**
 * Resolves a URL or host-like string to a canonical DNS name. URL parsing is
 * used for host strings too, so alternate IPv4 spellings are canonicalized
 * before the IP-literal check.
 */
export function canonicalizeHost(input: string | URL): string | null {
  const parsed = parseAbsoluteUrl(input);
  if (parsed !== null) {
    return stripHostnameDecorations(parsed.hostname);
  }

  if (typeof input !== "string") {
    return null;
  }

  const candidate = input.trim();
  if (candidate === "") {
    return null;
  }

  try {
    const hostUrl = new URL(`https://${candidate}`);
    return stripHostnameDecorations(hostUrl.hostname);
  } catch {
    return stripHostnameDecorations(candidate);
  }
}

export function isIpLiteral(hostname: string): boolean {
  const normalized = stripHostnameDecorations(hostname);
  return normalized.includes(":") || IPV4_LITERAL.test(normalized);
}

function isDnsHostname(hostname: string): boolean {
  return hostname !== "" && !isIpLiteral(hostname);
}

/**
 * In-memory HSTS policy store suitable for a browser network stack. Inject a
 * clock for deterministic expiration tests.
 */
export class HstsStore {
  private readonly entries = new Map<string, HstsEntry>();
  private readonly now: () => number;

  constructor(options: HstsStoreOptions = {}) {
    this.now = options.now ?? Date.now;
  }

  /**
   * Applies an STS response header. Non-HTTPS responses and IP-literal hosts
   * are ignored. Returns true only when a syntactically valid HSTS field was
   * processed.
   */
  processResponse(url: string | URL, headers: HstsHeaders | null | undefined): boolean {
    const parsedUrl = parseAbsoluteUrl(url);
    if (parsedUrl === null || parsedUrl.protocol !== "https:") {
      return false;
    }

    const hostname = stripHostnameDecorations(parsedUrl.hostname);
    if (!isDnsHostname(hostname)) {
      return false;
    }

    const field = readHstsHeader(headers);
    if (field === null) {
      return false;
    }

    const policy = parseHstsHeader(field);
    if (policy === null) {
      return false;
    }

    if (policy.maxAge === 0) {
      this.entries.delete(hostname);
      return true;
    }

    const receivedAt = this.now();
    const expiresAt = Math.min(
      Number.MAX_SAFE_INTEGER,
      receivedAt + policy.maxAge * 1000,
    );

    this.entries.set(hostname, {
      hostname,
      maxAge: policy.maxAge,
      includeSubDomains: policy.includeSubDomains,
      expiresAt,
    });
    return true;
  }

  /** Convenience alias for processResponse. */
  applyResponse(url: string | URL, headers: HstsHeaders | null | undefined): boolean {
    return this.processResponse(url, headers);
  }

  /** Returns the exact, unexpired policy for a host, if one exists. */
  getEntry(hostOrUrl: string | URL): Readonly<HstsEntry> | null {
    const hostname = this.lookupHostname(hostOrUrl);
    if (hostname === null) {
      return null;
    }

    const entry = this.entries.get(hostname);
    if (entry === undefined || entry.expiresAt <= this.now()) {
      return null;
    }

    return { ...entry };
  }

  /** Returns the nearest exact or includeSubDomains policy covering a host. */
  getPolicy(hostOrUrl: string | URL): Readonly<HstsEntry> | null {
    const hostname = this.lookupHostname(hostOrUrl);
    if (hostname === null) {
      return null;
    }

    return this.findCoveringEntry(hostname);
  }

  /** Whether the host is protected by an exact or ancestor HSTS policy. */
  isSecure(hostOrUrl: string | URL): boolean {
    return this.getPolicy(hostOrUrl) !== null;
  }

  /** Convenience alias for isSecure. */
  isHostSecure(hostOrUrl: string | URL): boolean {
    return this.isSecure(hostOrUrl);
  }

  /**
   * Deletes only an exact host's entry. It never removes an ancestor policy,
   * so a subdomain's max-age=0 cannot weaken its parent.
   */
  delete(hostOrUrl: string | URL): boolean {
    const hostname = this.lookupHostname(hostOrUrl);
    if (hostname === null) {
      return false;
    }

    return this.entries.delete(hostname);
  }

  clear(): void {
    this.entries.clear();
  }

  get size(): number {
    this.removeExpiredEntries();
    return this.entries.size;
  }

  private lookupHostname(hostOrUrl: string | URL): string | null {
    const hostname = canonicalizeHost(hostOrUrl);
    return hostname !== null && isDnsHostname(hostname) ? hostname : null;
  }

  private findCoveringEntry(hostname: string): Readonly<HstsEntry> | null {
    const now = this.now();
    let candidate = hostname;

    while (true) {
      const entry = this.entries.get(candidate);
      if (entry !== undefined && entry.expiresAt > now) {
        if (candidate === hostname || entry.includeSubDomains) {
          return { ...entry };
        }
      }

      const dotIndex = candidate.indexOf(".");
      if (dotIndex === -1) {
        return null;
      }
      candidate = candidate.slice(dotIndex + 1);
    }
  }

  private removeExpiredEntries(): void {
    const now = this.now();
    for (const [hostname, entry] of this.entries) {
      if (entry.expiresAt <= now) {
        this.entries.delete(hostname);
      }
    }
  }
}

export { HstsStore as HSTSStore };
