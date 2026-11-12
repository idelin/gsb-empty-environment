/**
 * Hostname / IP classification helpers.
 *
 * A forged SSDP response can point LOCATION at an internal management
 * interface (e.g. a cloud metadata address, a peer device's admin port) or
 * at a public host the control point would then access on the attacker's
 * behalf. The fetcher therefore only accepts literal IP addresses that are
 * private-use addresses. DNS names are never resolved here: the SSDP source
 * address is always an IP literal, so a name can never match it and would
 * only open a DNS-rebinding surface.
 */

const IPV4_REGEX =
  /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;

const IPV6_REGEX = /^[0-9a-fA-F:]+(:%[0-9a-zA-Z._-]+)?$/;

/** Parsed IPv4 address in numeric form, or null when not a valid IPv4 literal. */
export function parseIpv4(host: string): number[] | null {
  if (!IPV4_REGEX.test(host)) {
    return null;
  }
  return host.split(".").map((octet) => Number(octet));
}

function expandIpv6(host: string): number[] | null {
  let address = host;
  const percentIndex = address.indexOf("%");
  if (percentIndex !== -1) {
    address = address.slice(0, percentIndex);
  }
  if (!IPV6_REGEX.test(address) || (address.match(/::/g)?.length ?? 0) > 1) {
    return null;
  }

  const doubleColon = address.indexOf("::");
  let head: string[] = [];
  let tail: string[] = [];
  if (doubleColon !== -1) {
    head = address.slice(0, doubleColon).split(":").filter(Boolean);
    tail = address.slice(doubleColon + 2).split(":").filter(Boolean);
  } else {
    head = address.split(":");
  }

  const missing = 8 - head.length - tail.length;
  if (missing < 0 || (doubleColon === -1 && missing !== 0)) {
    return null;
  }
  const groups = [...head, ...Array(missing).fill("0"), ...tail];
  if (groups.length !== 8) {
    return null;
  }

  const result: number[] = [];
  for (const group of groups) {
    if (!/^[0-9a-fA-F]{1,4}$/.test(group)) {
      return null;
    }
    const value = parseInt(group, 16);
    result.push((value >> 8) & 0xff, value & 0xff);
  }
  return result;
}

/**
 * Strict RFC 1918 / RFC 4193 private-use check.
 *
 * Accepted IPv4 ranges: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16.
 * Accepted IPv6 ranges: unique-local fc00::/7.
 *
 * Deliberately excluded: loopback (127.0.0.0/8, ::1), link-local
 * (169.254.0.0/16, fe80::/10), CGNAT 100.64.0.0/10, multicast and any
 * public/globally routable address. An SSDP answer that appears to come
 * from one of those ranges is not a usable private device address.
 */
export function isPrivateAddress(host: string): boolean {
  const ipv4 = parseIpv4(host);
  if (ipv4 !== null) {
    const [first, second] = ipv4;
    if (first === 10) {
      return true;
    }
    if (first === 172 && second >= 16 && second <= 31) {
      return true;
    }
    if (first === 192 && second === 168) {
      return true;
    }
    return false;
  }

  const ipv6 = expandIpv6(host);
  if (ipv6 !== null) {
    return (ipv6[0] & 0xfe) === 0xfc;
  }
  return false;
}

/**
 * Returns true only for literal IP addresses (v4 or v6). Bracketed IPv6
 * forms from URLs must be unwrapped before calling.
 */
export function isIpLiteral(host: string): boolean {
  return parseIpv4(host) !== null || expandIpv6(host) !== null;
}

/**
 * Normalizes a hostname extracted from a URL so that equivalent
 * representations compare equal. IPv6 literals are lower-cased; IPv4
 * octets are validated and returned unchanged (no leading-zero or decimal
 * reinterpretation is performed).
 */
export function normalizeHost(host: string): string {
  if (parseIpv4(host) !== null) {
    return host;
  }
  const ipv6 = expandIpv6(host);
  if (ipv6 !== null) {
    const groups: string[] = [];
    for (let index = 0; index < 16; index += 2) {
      groups.push(((ipv6[index] << 8) | ipv6[index + 1]).toString(16));
    }
    return compressIpv6(groups);
  }
  return host.toLowerCase();
}

/** Applies RFC 5952-style "::" compression to 8 lowercase hex groups. */
function compressIpv6(groups: string[]): string {
  let bestStart = -1;
  let bestLength = 0;
  let runStart = -1;
  for (let index = 0; index <= groups.length; index += 1) {
    if (index < groups.length && groups[index] === "0") {
      if (runStart === -1) {
        runStart = index;
      }
    } else if (runStart !== -1) {
      const runLength = index - runStart;
      if (runLength > bestLength) {
        bestStart = runStart;
        bestLength = runLength;
      }
      runStart = -1;
    }
  }
  if (bestLength < 2) {
    return groups.join(":");
  }
  const head = groups.slice(0, bestStart).join(":");
  const tail = groups.slice(bestStart + bestLength).join(":");
  return `${head}::${tail}`;
}

/** Case-insensitive, representation-stable equality for two host literals. */
export function sameHost(left: string, right: string): boolean {
  return normalizeHost(left) === normalizeHost(right);
}
