export type FetchDirective =
  | "script-src"
  | "style-src"
  | "img-src"
  | "font-src";

export interface CspPolicy {
  directives: Map<string, string[]>;
}

/**
 * Parse a Content-Security-Policy header value into a directive map.
 * Directive names are lower-cased; source expressions are kept as-is and
 * matched case-insensitively where appropriate.
 */
export function parseCsp(header: string | null | undefined): CspPolicy {
  const directives = new Map<string, string[]>();
  if (!header) {
    return { directives };
  }
  for (const chunk of header.split(";")) {
    const trimmed = chunk.trim();
    if (!trimmed) continue;
    const [name, ...sources] = trimmed.split(/\s+/);
    directives.set(name.toLowerCase(), sources);
  }
  return { directives };
}

/**
 * Parse possibly multiple CSP header values. Per spec every policy is
 * enforced, so each header becomes its own policy and all must allow a
 * fetch for it to proceed.
 */
export function parseCspHeaders(
  headers: Array<string | null | undefined>,
): CspPolicy[] {
  return headers
    .filter((h): h is string => typeof h === "string" && h.trim() !== "")
    .map(parseCsp);
}

function normalizeUrl(raw: string): URL | null {
  try {
    return new URL(raw);
  } catch {
    return null;
  }
}

function hostMatches(pattern: string, host: string): boolean {
  const p = pattern.toLowerCase();
  const h = host.toLowerCase();
  if (p.startsWith("*.")) {
    const suffix = p.slice(1); // ".example.com"
    return h.endsWith(suffix) && h.length > suffix.length;
  }
  return p === h;
}

function sourceMatches(source: string, url: URL, selfOrigin: string): boolean {
  const s = source.toLowerCase();

  if (s === "'none'" || s === "'unsafe-inline'" || s === "'unsafe-eval'") {
    return false;
  }
  if (s === "'self'") {
    return url.origin === selfOrigin;
  }
  if (s === "*") {
    return url.protocol === "http:" || url.protocol === "https:";
  }
  // Scheme source, e.g. "https:" or "data:"
  if (/^[a-z][a-z0-9+.-]*:$/.test(s)) {
    return url.protocol === s;
  }

  // Host source, optionally with scheme and port:
  // "example.com", "*.example.com", "https://example.com",
  // "https://example.com:8443", "example.com:8080"
  let scheme: string | null = null;
  let rest = s;
  const schemeMatch = /^([a-z][a-z0-9+.-]*):\/\//.exec(s);
  if (schemeMatch) {
    scheme = schemeMatch[1] + ":";
    rest = s.slice(schemeMatch[0].length);
  }
  // Ignore any path component; host matching only.
  const slashIdx = rest.indexOf("/");
  if (slashIdx !== -1) {
    rest = rest.slice(0, slashIdx);
  }
  let port: string | null = null;
  const portMatch = /:(\d+|\*)$/.exec(rest);
  if (portMatch) {
    port = portMatch[1];
    rest = rest.slice(0, portMatch[0].length);
  }
  if (rest === "") return false;

  if (scheme !== null && url.protocol !== scheme) return false;
  if (!hostMatches(rest, url.hostname)) return false;
  if (port !== null && port !== "*") {
    const urlPort =
      url.port !== ""
        ? url.port
        : url.protocol === "https:"
          ? "443"
          : url.protocol === "http:"
            ? "80"
            : "";
    if (urlPort !== port) return false;
  }
  return true;
}

/**
 * Check whether a single policy allows `url` for the given fetch
 * directive, falling back to default-src when the directive is absent.
 */
export function policyAllows(
  policy: CspPolicy,
  directive: FetchDirective,
  rawUrl: string,
  selfOrigin: string,
): boolean {
  const url = normalizeUrl(rawUrl);
  if (url === null) return false;

  const sources =
    policy.directives.get(directive) ?? policy.directives.get("default-src");
  // No applicable directive: the policy does not restrict this fetch type.
  if (sources === undefined) return true;
  if (sources.some((s) => s.toLowerCase() === "'none'")) return false;
  return sources.some((s) => sourceMatches(s, url, selfOrigin));
}

/**
 * Check all enforced policies (from possibly multiple CSP headers).
 * The URL must be allowed by every policy.
 */
export function cspAllows(
  policies: CspPolicy[],
  directive: FetchDirective,
  url: string,
  selfOrigin: string,
): boolean {
  return policies.every((p) => policyAllows(p, directive, url, selfOrigin));
}
