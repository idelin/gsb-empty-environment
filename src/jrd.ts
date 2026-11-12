import type { Jrd, JrdLink } from "./types.ts";
import {
  hostnameOf,
  hostOfUrl,
  normalizeHost,
  tryNormalizeHost,
  tryParseAcct,
} from "./uri.ts";

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasRelation(rel: string, expected: string): boolean {
  return rel
    .split(/\s+/)
    .some((token) => token.toLowerCase() === expected);
}

function normalizeWhitelist(allowedSelfHosts: readonly string[]): Set<string> {
  const allowed = new Set<string>();
  for (const entry of allowedSelfHosts) {
    const normalized = tryNormalizeHost(entry);
    if (normalized) {
      allowed.add(normalized);
    }
  }
  return allowed;
}

function resolveLinkHost(href: string, queryHost: string): string | null {
  let parsed: URL;
  try {
    parsed = new URL(href, `https://${queryHost}/`);
  } catch {
    return null;
  }

  if (
    (parsed.protocol !== "https:" && parsed.protocol !== "http:") ||
    parsed.username ||
    parsed.password
  ) {
    return null;
  }
  return hostOfUrl(parsed);
}

function filterLinks(
  links: readonly unknown[],
  queryHost: string,
  allowedSelfHosts: Set<string>,
): JrdLink[] {
  const filtered: JrdLink[] = [];

  for (const value of links) {
    if (!isRecord(value) || typeof value.rel !== "string") {
      continue;
    }

    const link = { ...value } as unknown as JrdLink;
    if (hasRelation(value.rel, "self")) {
      if (typeof value.href !== "string") {
        continue;
      }

      const hrefHost = resolveLinkHost(value.href, queryHost);
      if (!hrefHost) {
        continue;
      }
      if (hrefHost !== queryHost && !allowedSelfHosts.has(hrefHost)) {
        continue;
      }
    }

    filtered.push(link);
  }

  return filtered;
}

/**
 * Validate a JRD before it can be used for account resolution.
 *
 * A missing/invalid `subject`, a subject that is not an `acct:` URI, or a
 * subject whose host differs from the queried host causes the entire document
 * to be rejected. External `rel=self` links are dropped unless their authority
 * appears in `allowedSelfHosts`.
 */
export function validateJrd(
  input: unknown,
  queryHost: string,
  allowedSelfHosts: readonly string[] = [],
): Jrd | null {
  let normalizedQueryHost: string;
  try {
    normalizedQueryHost = normalizeHost(queryHost);
  } catch {
    return null;
  }

  if (!isRecord(input) || typeof input.subject !== "string") {
    return null;
  }

  const subject = tryParseAcct(input.subject);
  if (!subject || subject.hostname !== hostnameOf(normalizedQueryHost)) {
    return null;
  }

  let links: JrdLink[] | undefined;
  if (input.links !== undefined) {
    if (!Array.isArray(input.links)) {
      return null;
    }
    links = filterLinks(
      input.links,
      normalizedQueryHost,
      normalizeWhitelist(allowedSelfHosts),
    );
  }

  return {
    ...input,
    subject: input.subject,
    ...(links === undefined ? {} : { links }),
  } as unknown as Jrd;
}
