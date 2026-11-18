const V4_RANGES: Array<[number, number]> = [
  [0x00000000, 8],
  [0x0a000000, 8],
  [0x64400000, 10],
  [0x7f000000, 8],
  [0xa9fe0000, 16],
  [0xac100000, 12],
  [0xc0000000, 24],
  [0xc0000200, 24],
  [0xc0a80000, 16],
  [0xc6120000, 15],
  [0xc6336400, 24],
  [0xcb007100, 24],
  [0xe0000000, 4],
  [0xf0000000, 4],
];

export function parseIPv4(host: string): number[] | null {
  const parts = host.split('.');
  if (parts.length !== 4) return null;
  const nums: number[] = [];
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part)) return null;
    const n = Number(part);
    if (n > 255) return null;
    nums.push(n);
  }
  return nums;
}

export function isPrivateIPv4(host: string): boolean {
  const parts = parseIPv4(host);
  if (!parts) return false;
  const value =
    ((parts[0] * 2 ** 24 + parts[1] * 2 ** 16 + parts[2] * 2 ** 8 + parts[3]) >>>
      0);
  for (const [network, prefix] of V4_RANGES) {
    const mask = prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0;
    if (((value & mask) >>> 0) === network) return true;
  }
  return false;
}

function parseHextets(part: string): number[] | null {
  if (part === '') return [];
  const groups = part.split(':');
  const out: number[] = [];
  for (const group of groups) {
    if (!/^[0-9a-f]{1,4}$/.test(group)) return null;
    out.push(parseInt(group, 16));
  }
  return out;
}

export function parseIPv6(input: string): number[] | null {
  let s = input.toLowerCase();
  if (s.startsWith('[') && s.endsWith(']')) s = s.slice(1, -1);
  if (!s.includes(':')) return null;
  let v4Hextets: number[] = [];
  const lastGroup = s.split(':').pop() ?? '';
  if (lastGroup.includes('.')) {
    const v4 = parseIPv4(lastGroup);
    if (!v4) return null;
    v4Hextets = [(v4[0] << 8) | v4[1], (v4[2] << 8) | v4[3]];
    s = s.slice(0, s.length - lastGroup.length);
    if (s.endsWith(':') && !s.endsWith('::')) s = s.slice(0, -1);
  }
  const dc = s.indexOf('::');
  if (dc === -1) {
    const all = parseHextets(s);
    if (!all) return null;
    const full = [...all, ...v4Hextets];
    return full.length === 8 ? full : null;
  }
  if (s.indexOf('::', dc + 2) !== -1) return null;
  const left = parseHextets(s.slice(0, dc));
  const right = parseHextets(s.slice(dc + 2));
  if (!left || !right) return null;
  const zeros = 8 - (left.length + right.length + v4Hextets.length);
  if (zeros < 0) return null;
  return [...left, ...new Array(zeros).fill(0), ...right, ...v4Hextets];
}

export function isPrivateIPv6(input: string): boolean {
  const h = parseIPv6(input);
  if (!h) return false;
  if (h.every((x) => x === 0)) return true;
  if (h.slice(0, 7).every((x) => x === 0) && h[7] === 1) return true;
  if (h.slice(0, 5).every((x) => x === 0) && h[5] === 0xffff) {
    const v4 = [
      (h[6] >> 8) & 0xff,
      h[6] & 0xff,
      (h[7] >> 8) & 0xff,
      h[7] & 0xff,
    ];
    return isPrivateIPv4(v4.join('.'));
  }
  if ((h[0] & 0xfe00) === 0xfc00) return true;
  if ((h[0] & 0xffc0) === 0xfe80) return true;
  if ((h[0] & 0xff00) === 0xff00) return true;
  if (h[0] === 0x2001 && h[1] === 0x0db8) return true;
  if (h[0] === 0x0100) return true;
  return false;
}

export function isPrivateAddress(host: string): boolean {
  let h = host.toLowerCase();
  if (h.startsWith('[') && h.endsWith(']')) h = h.slice(1, -1);
  if (h.includes(':')) return isPrivateIPv6(h);
  return isPrivateIPv4(h);
}
