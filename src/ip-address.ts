/**
 * IP literal parsing and address-space classification.
 *
 * Handles the obfuscated forms browsers accept in URLs: decimal/hex/octal
 * IPv4 integers ("2130706433", "0x7f000001", "0177.0.0.1", "127.1") and
 * IPv4-mapped / IPv4-compatible IPv6 ("::ffff:127.0.0.1", "::7f00:1").
 */

export type AddressSpace = "public" | "private" | "loopback" | "link-local";

/**
 * Parse an IPv4 address per the WHATWG URL rules: 1-4 dot-separated
 * parts, each decimal, octal (leading 0) or hex (leading 0x); the last
 * part fills the remaining bytes. Returns the 32-bit address or null.
 */
export function parseIPv4(input: string): number | null {
  let s = input;
  if (s.endsWith(".")) s = s.slice(0, -1);
  if (s === "") return null;
  const parts = s.split(".");
  if (parts.length > 4) return null;
  const nums: number[] = [];
  for (const part of parts) {
    if (part === "") return null;
    let n: number;
    if (part.length > 2 && (part.startsWith("0x") || part.startsWith("0X"))) {
      if (!/^[0-9a-fA-F]+$/.test(part.slice(2))) return null;
      n = parseInt(part.slice(2), 16);
    } else if (part.length > 1 && part.startsWith("0")) {
      if (!/^[0-7]+$/.test(part.slice(1))) return null;
      n = parseInt(part.slice(1), 8);
    } else {
      if (!/^[0-9]+$/.test(part)) return null;
      n = parseInt(part, 10);
    }
    nums.push(n);
  }
  for (let i = 0; i < nums.length - 1; i++) {
    if (nums[i] > 255) return null;
  }
  const last = nums[nums.length - 1];
  if (last >= 256 ** (5 - nums.length)) return null;
  let result = last;
  for (let i = 0; i < nums.length - 1; i++) {
    result += nums[i] * 256 ** (3 - i);
  }
  return result >>> 0;
}

function isHexDigit(ch: string | undefined): boolean {
  return (
    ch !== undefined &&
    ((ch >= "0" && ch <= "9") ||
      (ch >= "a" && ch <= "f") ||
      (ch >= "A" && ch <= "F"))
  );
}

/**
 * Parse an IPv6 address (WHATWG URL rules, no zone IDs). Returns the
 * 16 bytes or null. Embedded IPv4 ("::ffff:1.2.3.4") is supported.
 */
export function parseIPv6(input: string): Uint8Array | null {
  const pieces = new Uint16Array(8);
  let pieceIndex = 0;
  let compress: number | null = null;
  let p = 0;

  if (input[p] === ":") {
    if (input[p + 1] !== ":") return null;
    p += 2;
    pieceIndex++;
    compress = pieceIndex;
  }

  while (p < input.length) {
    if (pieceIndex === 8) return null;
    if (input[p] === ":") {
      if (compress !== null) return null;
      p++;
      pieceIndex++;
      compress = pieceIndex;
      continue;
    }

    let value = 0;
    let length = 0;
    while (length < 4 && isHexDigit(input[p])) {
      value = value * 16 + parseInt(input[p], 16);
      p++;
      length++;
    }

    if (input[p] === ".") {
      if (length === 0) return null;
      p -= length;
      if (pieceIndex > 6) return null;
      let numbersSeen = 0;
      while (p < input.length) {
        if (numbersSeen > 0) {
          if (input[p] === "." && numbersSeen < 4) {
            p++;
          } else {
            return null;
          }
        }
        if (!/[0-9]/.test(input[p])) return null;
        let ipv4Piece = "";
        while (p < input.length && /[0-9]/.test(input[p])) {
          ipv4Piece += input[p];
          p++;
        }
        if (ipv4Piece.length === 0) return null;
        if (ipv4Piece.length > 1 && ipv4Piece.startsWith("0")) return null;
        const n = Number(ipv4Piece);
        if (n > 255) return null;
        pieces[pieceIndex] = pieces[pieceIndex] * 0x100 + n;
        numbersSeen++;
        if (numbersSeen === 2 || numbersSeen === 4) pieceIndex++;
      }
      if (numbersSeen !== 4) return null;
      break;
    } else if (input[p] === ":") {
      p++;
      if (p >= input.length) return null;
    } else if (p < input.length) {
      return null;
    }
    pieces[pieceIndex] = value;
    pieceIndex++;
  }

  if (compress !== null) {
    let swaps = pieceIndex - compress;
    pieceIndex = 7;
    while (swaps > 0) {
      const tmp = pieces[pieceIndex];
      pieces[pieceIndex] = pieces[compress + swaps - 1];
      pieces[compress + swaps - 1] = tmp;
      pieceIndex--;
      swaps--;
    }
  } else if (pieceIndex !== 8) {
    return null;
  }

  const bytes = new Uint8Array(16);
  for (let i = 0; i < 8; i++) {
    bytes[i * 2] = pieces[i] >> 8;
    bytes[i * 2 + 1] = pieces[i] & 0xff;
  }
  return bytes;
}

export function classifyIPv4(ip: number): AddressSpace {
  const b0 = (ip >>> 24) & 0xff;
  const b1 = (ip >>> 16) & 0xff;
  // 0.0.0.0/8 ("this host on this network") reaches loopback on most
  // stacks, so treat it as loopback.
  if (b0 === 0 || b0 === 127) return "loopback";
  if (b0 === 10) return "private";
  if (b0 === 172 && b1 >= 16 && b1 <= 31) return "private";
  if (b0 === 192 && b1 === 168) return "private";
  if (b0 === 169 && b1 === 254) return "link-local";
  return "public";
}

export function classifyIPv6(bytes: Uint8Array): AddressSpace {
  const allZero = bytes.every((b) => b === 0);
  if (allZero) return "loopback"; // :: unspecified
  const isLoopback = bytes.slice(0, 15).every((b) => b === 0) && bytes[15] === 1;
  if (isLoopback) return "loopback"; // ::1
  const firstTenZero = bytes.slice(0, 10).every((b) => b === 0);
  // IPv4-mapped ::ffff:0:0/96 and deprecated IPv4-compatible ::/96.
  if (firstTenZero && ((bytes[10] === 0xff && bytes[11] === 0xff) || bytes[10] === 0)) {
    const v4 =
      ((bytes[12] << 24) | (bytes[13] << 16) | (bytes[14] << 8) | bytes[15]) >>> 0;
    return classifyIPv4(v4);
  }
  if ((bytes[0] & 0xfe) === 0xfc) return "private"; // fc00::/7 unique local
  if (bytes[0] === 0xfe && (bytes[1] & 0xc0) === 0x80) return "link-local"; // fe80::/10
  return "public";
}

/**
 * Classify a host if it is an IP literal (with or without IPv6 brackets).
 * Returns null when the host is a DNS name rather than an IP literal.
 */
export function classifyHost(host: string): AddressSpace | null {
  let h = host;
  if (h.startsWith("[") && h.endsWith("]")) h = h.slice(1, -1);
  const v4 = parseIPv4(h);
  if (v4 !== null) return classifyIPv4(v4);
  const v6 = parseIPv6(h);
  if (v6 !== null) return classifyIPv6(v6);
  return null;
}
