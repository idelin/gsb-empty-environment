function parseIpv4(address: string): bigint | null {
  const parts = address.split(".");
  if (parts.length !== 4) return null;
  let value = 0n;
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part)) return null;
    const octet = Number(part);
    if (octet > 255) return null;
    value = (value << 8n) | BigInt(octet);
  }
  return value;
}

function parseIpv6(address: string): bigint | null {
  let text = address.toLowerCase();
  const zoneIndex = text.indexOf("%");
  if (zoneIndex !== -1) text = text.slice(0, zoneIndex);

  const v4Index = text.lastIndexOf(".");
  if (v4Index !== -1) {
    const colonIndex = text.lastIndexOf(":", v4Index);
    if (colonIndex === -1) return null;
    const v4 = parseIpv4(text.slice(colonIndex + 1));
    if (v4 === null) return null;
    const hi = ((v4 >> 16n) & 0xffffn).toString(16);
    const lo = (v4 & 0xffffn).toString(16);
    text = text.slice(0, colonIndex + 1) + hi + ":" + lo;
  }

  const halves = text.split("::");
  if (halves.length > 2) return null;
  const left = halves[0] === "" ? [] : halves[0].split(":");
  const right =
    halves.length === 2 ? (halves[1] === "" ? [] : halves[1].split(":")) : [];
  if (halves.length === 1 && left.length !== 8) return null;
  if (left.length + right.length > 8) return null;
  const missing = 8 - left.length - right.length;
  const groups = [...left, ...Array<string>(missing).fill("0"), ...right];
  if (groups.length !== 8) return null;

  let value = 0n;
  for (const group of groups) {
    if (!/^[0-9a-f]{1,4}$/.test(group)) return null;
    value = (value << 16n) | BigInt(parseInt(group, 16));
  }
  return value;
}

export function parseIp(address: string): { bits: bigint; size: bigint } | null {
  const v4 = parseIpv4(address);
  if (v4 !== null) return { bits: v4, size: 32n };
  const v6 = parseIpv6(address);
  if (v6 !== null) return { bits: v6, size: 128n };
  return null;
}

export interface Network {
  bits: bigint;
  size: bigint;
  prefix: bigint;
}

export function parseNetwork(cidr: string): Network | null {
  const slash = cidr.indexOf("/");
  if (slash === -1) return null;
  const ip = parseIp(cidr.slice(0, slash).trim());
  if (ip === null) return null;
  const prefixText = cidr.slice(slash + 1).trim();
  if (!/^\d{1,3}$/.test(prefixText)) return null;
  const prefix = BigInt(Number(prefixText));
  if (prefix > ip.size) return null;
  return { bits: ip.bits, size: ip.size, prefix };
}

export function networkContains(network: Network, address: string): boolean {
  const ip = parseIp(address);
  if (ip === null || ip.size !== network.size) return false;
  const shift = network.size - network.prefix;
  return network.bits >> shift === ip.bits >> shift;
}
