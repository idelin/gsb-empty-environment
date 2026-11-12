/**
 * Classifies URL hostnames into address spaces so that report delivery to
 * loopback, private, or link-local addresses can be blocked unless the
 * document itself lives in the same address space.
 *
 * Handles the obfuscated forms an attacker might use in an endpoint URL:
 * integer IPv4 (decimal/octal/hex, 1-4 parts, e.g. "2130706433",
 * "0x7f000001", "0177.0.0.1", "127.1") and IPv4-mapped IPv6
 * ("[::ffff:127.0.0.1]", "[::ffff:7f00:1]").
 */

export type AddressSpace = "loopback" | "private" | "link-local" | "public";

export function classifyHost(hostname: string): AddressSpace {
  let host = hostname.toLowerCase();
  if (host.startsWith("[") && host.endsWith("]")) {
    host = host.slice(1, -1);
  }
  if (host.includes(":")) {
    return classifyIPv6(host);
  }
  const ipv4 = parseIPv4(host);
  if (ipv4 !== null) {
    return classifyIPv4(ipv4);
  }
  return "public"; // a domain name; DNS resolution is out of scope
}

function classifyIPv4(addr: number): AddressSpace {
  const b0 = (addr >>> 24) & 0xff;
  const b1 = (addr >>> 16) & 0xff;
  if (b0 === 127) return "loopback"; // 127.0.0.0/8
  if (b0 === 10) return "private"; // 10.0.0.0/8
  if (b0 === 172 && b1 >= 16 && b1 <= 31) return "private"; // 172.16.0.0/12
  if (b0 === 192 && b1 === 168) return "private"; // 192.168.0.0/16
  if (b0 === 169 && b1 === 254) return "link-local"; // 169.254.0.0/16
  return "public";
}

function classifyIPv6(input: string): AddressSpace {
  let addr = input;
  let embeddedIPv4: number | null = null;
  if (addr.includes(".")) {
    const lastColon = addr.lastIndexOf(":");
    if (lastColon < 0) return "public";
    embeddedIPv4 = parseIPv4(addr.slice(lastColon + 1));
    if (embeddedIPv4 === null) return "public";
    const hi = (embeddedIPv4 >>> 16).toString(16);
    const lo = (embeddedIPv4 & 0xffff).toString(16);
    addr = `${addr.slice(0, lastColon)}:${hi}:${lo}`;
  }
  const groups = parseIPv6Groups(addr);
  if (groups === null) return "public";
  // IPv4-mapped ::ffff:0:0/96 — classify by the embedded address.
  if (
    groups[0] === 0 && groups[1] === 0 && groups[2] === 0 &&
    groups[3] === 0 && groups[4] === 0 && groups[5] === 0xffff
  ) {
    return classifyIPv4((((groups[6] << 16) | groups[7]) >>> 0));
  }
  if (groups.every((g) => g === 0)) return "loopback"; // :: unspecified
  if (groups.slice(0, 7).every((g) => g === 0) && groups[7] === 1) {
    return "loopback"; // ::1
  }
  if ((groups[0] & 0xfe00) === 0xfc00) return "private"; // fc00::/7 (ULA)
  if ((groups[0] & 0xffc0) === 0xfe80) return "link-local"; // fe80::/10
  return "public";
}

/** Parses an IPv4 address in inet_aton form; returns a 32-bit number. */
function parseIPv4(input: string): number | null {
  const parts = input.split(".");
  if (parts.length === 0 || parts.length > 4) return null;
  const nums: number[] = [];
  for (const part of parts) {
    let n: number;
    if (/^0x[0-9a-f]+$/i.test(part)) {
      n = Number.parseInt(part.slice(2), 16);
    } else if (/^0[0-7]+$/.test(part)) {
      n = Number.parseInt(part, 8);
    } else if (/^[0-9]+$/.test(part)) {
      n = Number.parseInt(part, 10);
    } else {
      return null;
    }
    if (n > 0xffffffff) return null;
    nums.push(n);
  }
  // inet_aton packing: the last part fills all remaining bytes.
  const lastLimits = [0xffffffff, 0xffffff, 0xffff, 0xff];
  const last = nums[nums.length - 1];
  if (last > lastLimits[nums.length - 1]) return null;
  let result = last;
  for (let i = 0; i < nums.length - 1; i++) {
    if (nums[i] > 0xff) return null;
    result += nums[i] * 2 ** (8 * (3 - i));
  }
  return result >>> 0;
}

/** Parses an IPv6 address into eight 16-bit groups, or null if invalid. */
function parseIPv6Groups(input: string): number[] | null {
  const halves = input.split("::");
  if (halves.length > 2) return null;
  const head = halves[0] === "" ? [] : halves[0].split(":");
  const tail = halves.length === 2
    ? (halves[1] === "" ? [] : halves[1].split(":"))
    : [];
  for (const group of [...head, ...tail]) {
    if (!/^[0-9a-f]{1,4}$/i.test(group)) return null;
  }
  if (halves.length === 1 && head.length !== 8) return null;
  if (halves.length === 2 && head.length + tail.length > 7) return null;
  const groups = head.map((g) => Number.parseInt(g, 16));
  const missing = 8 - head.length - tail.length;
  for (let i = 0; i < missing; i++) groups.push(0);
  groups.push(...tail.map((g) => Number.parseInt(g, 16)));
  return groups;
}
