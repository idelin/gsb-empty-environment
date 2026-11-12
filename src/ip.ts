import { InvalidIpError } from './errors.ts';

export type IpSpace = 'loopback' | 'private' | 'public';

export type IpVersion = 4 | 6;

export interface ParsedIp {
  /** Raw numeric bytes (4 for IPv4, 16 for IPv6). */
  readonly bytes: Uint8Array;
  /** Wire version of the literal that was parsed. */
  readonly version: IpVersion;
  /**
   * True when an IPv6 literal embedded an IPv4 address
   * (::ffff:a.b.c.d mapped, or the deprecated ::a.b.c.d compatible form).
   */
  readonly embeddedV4: boolean;
}

/**
 * Parse and classify strictly, without relying on the host name: the caller
 * must supply the actual peer IP observed on the connection.
 */

export function parseIp(input: string): ParsedIp {
  if (typeof input !== 'string') {
    throw new InvalidIpError(input);
  }
  const literal = input.trim().toLowerCase();
  if (literal.length === 0) {
    throw new InvalidIpError(input);
  }

  if (literal.includes('.')) {
    if (literal.includes(':')) {
      return parseV6WithEmbeddedV4(input, literal);
    }
    return { bytes: parseV4(input, literal), version: 4, embeddedV4: false };
  }

  return parsePlainV6(input, literal);
}

function parseV4(original: string, literal: string): Uint8Array {
  const parts = literal.split('.');
  if (parts.length !== 4) {
    throw new InvalidIpError(original);
  }
  const bytes = new Uint8Array(4);
  for (let i = 0; i < 4; i += 1) {
    const part = parts[i];
    if (!/^\d{1,3}$/.test(part)) {
      throw new InvalidIpError(original);
    }
    const value = Number(part);
    if (value > 255 || (part.length > 1 && part.startsWith('0'))) {
      throw new InvalidIpError(original);
    }
    bytes[i] = value;
  }
  return bytes;
}

function parsePlainV6(original: string, literal: string): ParsedIp {
  if (literal.includes('.')) {
    throw new InvalidIpError(original);
  }
  if ((literal.match(/::/g)?.length ?? 0) > 1) {
    throw new InvalidIpError(original);
  }

  let groups: number[];
  const [leftRaw, rightRaw] = splitOnce(literal, '::');
  if (rightRaw === undefined) {
    const parts = literal.split(':');
    if (parts.length !== 8 || parts.some((group) => group === '')) {
      throw new InvalidIpError(original);
    }
    groups = parts.map((group) => parseGroup(group, original));
  } else {
    const left = leftRaw === '' ? [] : leftRaw.split(':');
    const right = rightRaw === '' ? [] : rightRaw.split(':');
    if (left.some((group) => group === '') || right.some((group) => group === '')) {
      throw new InvalidIpError(original);
    }
    if (left.length + right.length > 8) {
      throw new InvalidIpError(original);
    }
    groups = left.map((group) => parseGroup(group, original));
    while (groups.length + right.length < 8) {
      groups.push(0);
    }
    groups.push(...right.map((group) => parseGroup(group, original)));
  }
  if (groups.length !== 8) {
    throw new InvalidIpError(original);
  }

  const bytes = new Uint8Array(16);
  for (let i = 0; i < 8; i += 1) {
    bytes[i * 2] = groups[i] >> 8;
    bytes[i * 2 + 1] = groups[i] & 0xff;
  }
  return { bytes, version: 6, embeddedV4: false };
}

function parseV6WithEmbeddedV4(original: string, literal: string): ParsedIp {
  const lastColon = literal.lastIndexOf(':');
  const v6Part = literal.slice(0, lastColon + 1);
  const v4Part = literal.slice(lastColon + 1);
  const v4Bytes = parseV4(original, v4Part);

  const padded = `${v6Part}0:0`;
  const parsed = parsePlainV6(original, padded);
  const bytes = parsed.bytes;
  bytes[12] = v4Bytes[0];
  bytes[13] = v4Bytes[1];
  bytes[14] = v4Bytes[2];
  bytes[15] = v4Bytes[3];
  return { bytes, version: 6, embeddedV4: true };
}

function parseGroup(group: string, original: string): number {
  if (!/^[0-9a-f]{1,4}$/.test(group)) {
    throw new InvalidIpError(original);
  }
  return parseInt(group, 16);
}

function splitOnce(value: string, separator: string): [string | undefined, string | undefined] {
  const index = value.indexOf(separator);
  if (index === -1) {
    return [value, undefined];
  }
  return [value.slice(0, index), value.slice(index + separator.length)];
}

/**
 * Canonical string form. Mapped/compatible literals are unwrapped to the
 * embedded dotted IPv4 form first, so `::ffff:192.168.0.1` and
 * `192.168.0.1` produce identical cache keys.
 */
export function normalizeIp(input: string): string {
  const parsed = parseIp(input);
  if (parsed.embeddedV4) {
    return formatV4(parsed.bytes.slice(12));
  }
  if (parsed.version === 4) {
    return formatV4(parsed.bytes);
  }
  return formatV6(parsed.bytes);
}

function formatV4(bytes: Uint8Array): string {
  return `${bytes[0]}.${bytes[1]}.${bytes[2]}.${bytes[3]}`;
}

function formatV6(bytes: Uint8Array): string {
  const groups: number[] = [];
  for (let i = 0; i < 8; i += 1) {
    groups.push((bytes[i * 2] << 8) | bytes[i * 2 + 1]);
  }

  let bestStart = -1;
  let bestLength = 0;
  let runStart = -1;
  for (let i = 0; i <= 8; i += 1) {
    if (i < 8 && groups[i] === 0) {
      if (runStart === -1) {
        runStart = i;
      }
    } else if (runStart !== -1) {
      const runLength = i - runStart;
      if (runLength > bestLength) {
        bestStart = runStart;
        bestLength = runLength;
      }
      runStart = -1;
    }
  }

  const head = groups
    .slice(0, bestStart === -1 ? 8 : bestStart)
    .map((group) => group.toString(16))
    .join(':');
  if (bestStart === -1) {
    return head;
  }
  const tail = groups
    .slice(bestStart + bestLength)
    .map((group) => group.toString(16))
    .join(':');
  return `${head}::${tail}`;
}

/**
 * Reduce an IPv6 literal to pure IPv4 bytes when it is an IPv4-mapped
 * (::ffff:a.b.c.d) or legacy IPv4-compatible (::a.b.c.d) address. The
 * mapping is undone before classification, so a mapped literal can never
 * sneak a private/loopback IPv4 past an IPv6-only check.
 */
export function unwrapMappedV4(parsed: ParsedIp): ParsedIp | undefined {
  const bytes = parsed.bytes;
  const prefixIsZero =
    bytes[0] === 0 &&
    bytes[1] === 0 &&
    bytes[2] === 0 &&
    bytes[3] === 0 &&
    bytes[4] === 0 &&
    bytes[5] === 0 &&
    bytes[6] === 0 &&
    bytes[7] === 0 &&
    bytes[8] === 0 &&
    bytes[9] === 0;
  if (parsed.version !== 6 || !prefixIsZero) {
    return undefined;
  }

  const isMapped = bytes[10] === 0xff && bytes[11] === 0xff;
  // :: (unspecified) and ::1 (loopback) are native IPv6 addresses, never
  // IPv4-compatible mappings of 0.0.0.0 or 0.0.0.1.
  const hasCompatibleV4 =
    bytes[12] !== 0 || bytes[13] !== 0 || bytes[14] !== 0 || bytes[15] >= 2;
  if (!isMapped && !(hasCompatibleV4 && bytes[10] === 0 && bytes[11] === 0)) {
    return undefined;
  }

  return {
    bytes: bytes.slice(12),
    version: 4,
    embeddedV4: true,
  };
}

/**
 * Classify the actual peer IP into one of the three Private Network Access
 * address spaces. Everything that is not loopback and not globally routable
 * is treated as private (link-local, unique-local, RFC 1918, reserved,
 * multicast, unspecified), which is the conservative browser behavior.
 */
export function classifyIp(input: string): IpSpace {
  const parsed = parseIp(input);
  const unwrapped = unwrapMappedV4(parsed);
  const effective = unwrapped ?? parsed;

  if (effective.version === 4) {
    return classifyV4(effective.bytes);
  }
  return classifyV6(effective.bytes);
}

function classifyV4(bytes: Uint8Array): IpSpace {
  const [a, b] = bytes;

  if (a === 127) {
    return 'loopback';
  }
  if (a === 0) {
    return 'private';
  }
  if (a === 10) {
    return 'private';
  }
  if (a === 128 && b === 0) {
    return 'private';
  }
  if (a === 169 && b === 254) {
    return 'private';
  }
  if (a === 172 && b >= 16 && b <= 31) {
    return 'private';
  }
  if (a === 192 && b === 0) {
    return 'private';
  }
  if (a === 192 && b === 168) {
    return 'private';
  }
  if (a === 198 && (b === 18 || b === 19)) {
    return 'private';
  }
  if (a >= 224) {
    return 'private';
  }
  return 'public';
}

function classifyV6(bytes: Uint8Array): IpSpace {
  const loopback = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1];
  if (loopback.every((byte, index) => bytes[index] === byte)) {
    return 'loopback';
  }
  if (bytes.every((byte) => byte === 0)) {
    return 'private';
  }
  if (bytes[0] === 0xfe && (bytes[1] & 0xc0) === 0x80) {
    return 'private';
  }
  if ((bytes[0] & 0xfe) === 0xfc) {
    return 'private';
  }
  if (bytes[0] === 0xff) {
    return 'private';
  }
  if (bytes[0] === 0x20 && bytes[1] === 0x01 && bytes[2] === 0x0d && bytes[3] === 0xb8) {
    return 'private';
  }
  if (
    bytes[0] === 0x20 &&
    bytes[1] === 0x01 &&
    bytes[2] === 0x00 &&
    bytes[3] === 0x00 &&
    bytes[4] === 0x00 &&
    bytes[5] === 0x02
  ) {
    return 'private';
  }
  return 'public';
}

/** Larger number means more publicly reachable. */
const SPACE_RANK: Record<IpSpace, number> = {
  loopback: 0,
  private: 1,
  public: 2,
};

export function spaceRank(space: IpSpace): number {
  return SPACE_RANK[space];
}

/**
 * A request needs a PNA preflight when the initiator lives in a more public
 * space than the target: public -> private/loopback and private -> loopback.
 * Same-space or less-public initiators (e.g. loopback -> public) do not.
 */
export function isMorePublic(initiator: IpSpace, target: IpSpace): boolean {
  return SPACE_RANK[initiator] > SPACE_RANK[target];
}
