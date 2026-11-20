export type Family = 4 | 6;

export interface ParsedIp {
  family: Family;
  value: bigint;
}

export interface ParsedCidr {
  family: Family;
  network: bigint;
  length: number;
}

const IPV4_MAPPED_HIGH = 0xffffn;

export function parseIPv4(input: string): number | null {
  const parts = input.split(".");
  if (parts.length !== 4) return null;
  let value = 0;
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part)) return null;
    const octet = Number(part);
    if (octet > 255) return null;
    value = value * 256 + octet;
  }
  return value;
}

export function parseIPv6(input: string): bigint | null {
  let text = input.toLowerCase();
  if (text.includes(".")) {
    const lastColon = text.lastIndexOf(":");
    if (lastColon < 0) return null;
    const v4 = parseIPv4(text.slice(lastColon + 1));
    if (v4 === null) return null;
    const hi = ((v4 >>> 16) & 0xffff).toString(16);
    const lo = (v4 & 0xffff).toString(16);
    text = `${text.slice(0, lastColon)}:${hi}:${lo}`;
  }
  const halves = text.split("::");
  if (halves.length > 2) return null;
  const head = halves[0] === "" ? [] : halves[0].split(":");
  const tail = halves.length === 2 ? (halves[1] === "" ? [] : halves[1].split(":")) : [];
  for (const group of [...head, ...tail]) {
    if (!/^[0-9a-f]{1,4}$/.test(group)) return null;
  }
  if (halves.length === 1 && head.length !== 8) return null;
  if (halves.length === 2 && head.length + tail.length >= 8) return null;
  const zeros = 8 - head.length - tail.length;
  const groups = [...head, ...new Array<string>(zeros).fill("0"), ...tail];
  let value = 0n;
  for (const group of groups) {
    value = (value << 16n) | BigInt(parseInt(group, 16));
  }
  return value;
}

export function parseIp(input: string): ParsedIp | null {
  if (input.includes(":")) {
    const value = parseIPv6(input);
    if (value === null) return null;
    if ((value >> 32n) === IPV4_MAPPED_HIGH) {
      return { family: 4, value: value & 0xffffffffn };
    }
    return { family: 6, value };
  }
  const v4 = parseIPv4(input);
  if (v4 === null) return null;
  return { family: 4, value: BigInt(v4) };
}

export function canonicalIp(input: string): string | null {
  const parsed = parseIp(input);
  if (!parsed) return null;
  if (parsed.family === 4) {
    const n = Number(parsed.value);
    return [(n >>> 24) & 255, (n >>> 16) & 255, (n >>> 8) & 255, n & 255].join(".");
  }
  const groups: string[] = [];
  for (let i = 0; i < 8; i++) {
    groups.push(((parsed.value >> BigInt((7 - i) * 16)) & 0xffffn).toString(16));
  }
  return groups.join(":");
}

export function parseCidr(cidr: string): ParsedCidr | null {
  const slash = cidr.indexOf("/");
  if (slash < 0) return null;
  const address = parseIp(cidr.slice(0, slash));
  if (!address) return null;
  const lengthText = cidr.slice(slash + 1);
  if (!/^\d{1,3}$/.test(lengthText)) return null;
  const length = Number(lengthText);
  const max = address.family === 4 ? 32 : 128;
  if (length > max) return null;
  return { family: address.family, network: address.value, length };
}

export function cidrContains(cidr: ParsedCidr, ip: ParsedIp): boolean {
  if (cidr.family !== ip.family) return false;
  const bits = cidr.family === 4 ? 32n : 128n;
  const shift = bits - BigInt(cidr.length);
  return cidr.network >> shift === ip.value >> shift;
}
