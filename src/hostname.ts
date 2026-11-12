/**
 * Hostname normalization and single-label wildcard matching (RFC 6125 §6.4.3).
 *
 * A wildcard is only accepted when `*` is the entire leftmost label, e.g.
 * `*.example.com`, and it matches exactly one label. `a.b.example.com`
 * therefore does not match `*.example.com`.
 */

export class HostnameError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "HostnameError";
  }
}

/** Lowercase, strip a single trailing dot and validate label structure. */
export function normalizeHostname(hostname: string): string {
  if (typeof hostname !== "string" || hostname.length === 0) {
    throw new HostnameError("hostname must be a non-empty string");
  }
  let value = hostname.toLowerCase();
  if (value.endsWith(".")) {
    value = value.slice(0, -1);
  }
  if (value.length === 0 || value.length > 253) {
    throw new HostnameError(`invalid hostname: ${hostname}`);
  }
  const labels = value.split(".");
  for (const label of labels) {
    if (
      label.length === 0 ||
      label.length > 63 ||
      !/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/.test(label)
    ) {
      throw new HostnameError(`invalid hostname label: ${label}`);
    }
  }
  return value;
}

export type NamePattern =
  | { kind: "exact"; value: string }
  | { kind: "wildcard"; suffix: string };

/**
 * Parse a policy/certificate name pattern. A wildcard is accepted only when
 * `*` is the whole leftmost label and at least one more label follows.
 */
export function parseNamePattern(pattern: string): NamePattern {
  if (pattern.startsWith("*")) {
    if (!pattern.startsWith("*.")) {
      throw new HostnameError(`wildcard must be the full leftmost label: ${pattern}`);
    }
    const suffix = normalizeHostname(pattern.slice(2));
    return { kind: "wildcard", suffix };
  }
  return { kind: "exact", value: normalizeHostname(pattern) };
}

/** Match a concrete hostname against a (possibly wildcard) name pattern. */
export function hostnameMatchesPattern(hostname: string, pattern: string): boolean {
  const target = normalizeHostname(hostname);
  const parsed = parseNamePattern(pattern);

  if (parsed.kind === "exact") {
    return target === parsed.value;
  }

  const suffix = `.${parsed.suffix}`;
  if (!target.endsWith(suffix)) {
    return false;
  }
  const prefix = target.slice(0, target.length - suffix.length);
  if (prefix.length === 0) {
    return false;
  }
  // Exactly one label: no further dots allowed.
  return !prefix.includes(".");
}

/** Match an MX host against one MX entry of an MTA-STS policy. */
export function mxMatchesPolicyEntry(mxHost: string, policyMx: string): boolean {
  return hostnameMatchesPattern(mxHost, policyMx);
}

/** Match an MX host against any of the DNS names presented in its certificate. */
export function certificateMatchesMx(mxHost: string, certificateNames: readonly string[]): boolean {
  return certificateNames.some((name) => {
    try {
      return hostnameMatchesPattern(mxHost, name);
    } catch {
      return false;
    }
  });
}
