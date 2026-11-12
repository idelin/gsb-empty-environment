/**
 * URL policy shared by description fetching, redirect handling and
 * controlURL/SOAP target resolution.
 *
 * The anchor of every comparison is the literal source IP of the SSDP
 * answer. LOCATION, every hop of a redirect chain, URLBase and every
 * controlURL must resolve back to that same private host: otherwise the
 * control point would be issuing requests to an attacker-chosen target.
 */

import {
  ControlUrlHostMismatchError,
  InvalidLocationError,
  NonPrivateHostError,
  RedirectHostMismatchError,
  UnsafePortError,
  UnsafeSchemeError,
  UrlBaseHostMismatchError,
} from "./errors.ts";
import {
  isIpLiteral,
  isPrivateAddress,
  parseIpv4,
  sameHost,
} from "./network.ts";

export interface ParsedHttpUrl {
  /** Lower-cased scheme, always "http" for accepted URLs. */
  scheme: "http";
  /** IPv4 literal or expanded IPv6 literal (no brackets). */
  host: string;
  /** Numeric port; URL without an explicit port means 80. */
  port: number;
  /** Path-and-query component, always at least "/". */
  path: string;
}

function unwrapHost(hostname: string): string {
  if (hostname.startsWith("[") && hostname.endsWith("]")) {
    return hostname.slice(1, -1);
  }
  return hostname;
}

/**
 * Extracts the host portion of the raw URL string and enforces that it is
 * an un-encoded, canonical IP literal. The WHATWG URL parser happily
 * accepts alternate radix / integer / trailing-dot forms (e.g.
 * http://2130706433/ or http://0x7f.0.0.1/) and normalizes them to a
 * different address, which would defeat the SSDP-source pinning.
 */
function extractRawHost(rawUrl: string): string {
  if (rawUrl.includes("\\")) {
    throw new InvalidLocationError(
      "Backslashes are not allowed in device URLs",
    );
  }
  const schemeMatch = /^([a-z][a-z0-9+.-]*):\/\//i.exec(rawUrl);
  if (schemeMatch === null) {
    throw new InvalidLocationError(`Malformed URL: ${truncate(rawUrl)}`);
  }
  const afterScheme = rawUrl.slice(schemeMatch[0].length);
  const authority = afterScheme.split(/[/?#]/, 1)[0];
  if (authority.includes("@")) {
    throw new InvalidLocationError(
      "Userinfo (credentials) is not allowed in device URLs",
    );
  }

  if (authority.startsWith("[")) {
    const closing = authority.indexOf("]");
    if (closing === -1) {
      throw new InvalidLocationError("Unterminated IPv6 literal");
    }
    const host = authority.slice(1, closing);
    if (!isIpLiteral(host) || host.includes("%")) {
      throw new InvalidLocationError(
        `Host must be an IP literal, got ${truncate(host)}`,
      );
    }
    return host;
  }

  const host = authority.split(":")[0];
  const ipv4 = parseIpv4(host);
  if (ipv4 === null) {
    throw new InvalidLocationError(
      `Host must be a canonical IPv4 or bracketed IPv6 literal, got ${truncate(host)}`,
    );
  }
  return host;
}

/**
 * Parses an absolute http:// URL whose host is a literal IP address.
 * DNS names, non-http schemes, credentials and non-numeric ports are
 * rejected. Does not perform any network I/O.
 */
export function parseHttpUrl(rawUrl: string): ParsedHttpUrl {
  const rawHost = extractRawHost(rawUrl);

  let parsed: URL;
  try {
    parsed = new URL(rawUrl);
  } catch {
    throw new InvalidLocationError(`Malformed URL: ${truncate(rawUrl)}`);
  }

  if (parsed.protocol !== "http:") {
    throw new UnsafeSchemeError(
      `Only plain http URLs are accepted, got ${parsed.protocol}`,
    );
  }
  if (parsed.username !== "" || parsed.password !== "") {
    throw new InvalidLocationError(
      "Userinfo (credentials) is not allowed in device URLs",
    );
  }

  const host = unwrapHost(decodeURIComponent(parsed.hostname));
  if (!sameHost(host, rawHost)) {
    throw new InvalidLocationError(
      `Canonicalized host ${host} differs from the literal address ${rawHost}`,
    );
  }

  // The WHATWG parser accepts empty/out-of-range-looking ports; validate
  // the raw authority segment explicitly so malformed ports fail closed.
  const authority = rawUrl.slice(rawUrl.indexOf("//") + 2).split(/[/?#]/, 1)[0];
  const portSegment = authority.slice(
    authority.startsWith("[")
      ? authority.indexOf("]") + 1
      : rawHost.length,
  );
  let port = 80;
  if (portSegment !== "") {
    if (!portSegment.startsWith(":")) {
      throw new UnsafePortError(`Malformed authority: ${truncate(authority)}`);
    }
    const rawPort = portSegment.slice(1);
    const candidate = Number(rawPort);
    if (!/^\d+$/.test(rawPort) || candidate < 1 || candidate > 65535) {
      throw new UnsafePortError(`Invalid port ${rawPort}`);
    }
    port = candidate;
  }

  const path = `${parsed.pathname}${parsed.search}` || "/";
  return { scheme: "http", host, port, path };
}

/**
 * Validates the LOCATION header of an SSDP response.
 *
 * @param location LOCATION header value from the SSDP answer.
 * @param ssdpSource literal source address of the SSDP datagram.
 */
export function validateLocation(
  location: string,
  ssdpSource: string,
): ParsedHttpUrl {
  if (!isIpLiteral(ssdpSource)) {
    throw new InvalidLocationError(
      `SSDP source must be an IP literal, got ${truncate(ssdpSource)}`,
    );
  }
  const parsed = parseHttpUrl(location);
  if (!sameHost(parsed.host, ssdpSource)) {
    throw new InvalidLocationError(
      `LOCATION host ${parsed.host} does not match SSDP source ${ssdpSource}`,
    );
  }
  if (!isPrivateAddress(parsed.host)) {
    throw new NonPrivateHostError(
      `SSDP source ${parsed.host} is not a private-use address`,
    );
  }
  return parsed;
}

/**
 * Validates one redirect hop. The Location header may be relative
 * (RFC 7231 allows it), in which case it is resolved against the current
 * URL first. The resolved host must still be the anchor host.
 */
export function validateRedirect(
  redirectTarget: string,
  anchorHost: string,
  currentUrl?: ParsedHttpUrl,
): ParsedHttpUrl {
  let candidate = redirectTarget;
  if (currentUrl !== undefined) {
    try {
      // Absolute URLs keep themselves; relative references resolve here.
      const resolved = new URL(redirectTarget, formatHttpUrl(currentUrl));
      candidate = resolved.href;
    } catch {
      // Fall through to parseHttpUrl which raises the typed error.
    }
  }
  const parsed = parseHttpUrl(candidate);
  if (!sameHost(parsed.host, anchorHost)) {
    throw new RedirectHostMismatchError(
      `Redirect to ${parsed.host} changes host; expected ${anchorHost}`,
    );
  }
  return parsed;
}

/**
 * Resolves a possibly relative reference against a base URL.
 *
 * URL resolution is performed with the WHATWG resolver, then the result is
 * re-validated against the anchor host. This catches absolute controlURLs
 * pointing at another internal machine and URLBase hijacking alike.
 */
function resolveAgainstBase(
  reference: string,
  base: string,
  anchorHost: string,
  ErrorClass:
    | typeof UrlBaseHostMismatchError
    | typeof ControlUrlHostMismatchError,
  label: string,
): ParsedHttpUrl {
  let absolute: string;
  try {
    absolute = new URL(reference, base).href;
  } catch {
    throw new InvalidLocationError(`Malformed ${label}: ${truncate(reference)}`);
  }
  const parsed = parseHttpUrl(absolute);
  if (!sameHost(parsed.host, anchorHost)) {
    throw new ErrorClass(
      `${label} resolves to host ${parsed.host}, expected ${anchorHost}`,
    );
  }
  return parsed;
}

/** Validates an optional URLBase element against the anchor host. */
export function validateUrlBase(
  urlBase: string | undefined,
  anchorHost: string,
): string | undefined {
  if (urlBase === undefined) {
    return undefined;
  }
  const parsed = resolveAgainstBase(
    urlBase,
    `http://${anchorHost}/`,
    anchorHost,
    UrlBaseHostMismatchError,
    "URLBase",
  );
  return formatHttpUrl(parsed);
}

/**
 * Resolves a service controlURL relative to the description base URL and
 * pins it to the anchor host.
 */
export function resolveControlUrl(
  controlUrl: string,
  baseUrl: string,
  anchorHost: string,
): ParsedHttpUrl {
  return resolveAgainstBase(
    controlUrl,
    baseUrl,
    anchorHost,
    ControlUrlHostMismatchError,
    "controlURL",
  );
}

/** Re-serializes a parsed URL in a stable form for request construction. */
export function formatHttpUrl(parsed: ParsedHttpUrl): string {
  const host = parsed.host.includes(":") ? `[${parsed.host}]` : parsed.host;
  const port = parsed.port === 80 ? "" : `:${parsed.port}`;
  return `http://${host}${port}${parsed.path}`;
}

function truncate(value: string): string {
  return value.length > 80 ? `${value.slice(0, 77)}...` : value;
}
