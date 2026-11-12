import { connect, isIP, type Socket, type SocketConnectOpts } from "node:net";
import { domainToASCII } from "node:url";

export type ParsedReferral = {
  hostname: string;
  port: 43;
};

export type ResolvedReferral = ParsedReferral & {
  address: string;
  family: 4 | 6;
};

export type DnsLookup = (
  hostname: string,
) => Promise<ReadonlyArray<{ address: string; family: 4 | 6 }>>;

export type ReferralSocketOptions = Omit<
  SocketConnectOpts,
  "host" | "port" | "family" | "lookup" | "path"
> & {
  timeout?: number;
};

export type ReferralSocketFactory = (
  options: SocketConnectOpts & { port: 43 },
) => Socket;

export type FollowOptions = {
  lookup?: DnsLookup;
  createSocket?: ReferralSocketFactory;
  socketOptions?: ReferralSocketOptions;
};

export class ReferralError extends Error {
  readonly code:
    | "INVALID_QUERY"
    | "INVALID_REFERRAL"
    | "NO_GLOBAL_UNICAST_ADDRESS"
    | "REFERRAL_LIMIT"
    | "REFERRAL_HOST_REVISITED";

  constructor(
    message: string,
    code:
      | "INVALID_QUERY"
      | "INVALID_REFERRAL"
      | "NO_GLOBAL_UNICAST_ADDRESS"
      | "REFERRAL_LIMIT"
      | "REFERRAL_HOST_REVISITED",
  ) {
    super(message);
    this.name = "ReferralError";
    this.code = code;
  }
}

const MAX_QUERY_BYTES = 255;
const MAX_REFERRALS = 3;
const REFERRAL_PATTERN =
  /^(?:referralserver|whois server)\s*:\s*(.*?)\s*$/i;

export function validateQuery(query: string): void {
  if (
    query.length === 0 ||
    query.includes("\r") ||
    query.includes("\n") ||
    query.includes("\0") ||
    query.startsWith("-") ||
    Buffer.byteLength(query, "utf8") > MAX_QUERY_BYTES
  ) {
    throw new ReferralError(
      "Whois query contains a prohibited character, option prefix, or is too long.",
      "INVALID_QUERY",
    );
  }
}

export function parseReferral(response: string): ParsedReferral | null {
  const lines = response.split(/\r\n|\r|\n/);
  const firstBlankLine = lines.findIndex((line) => line.trim() === "");
  const headerLines =
    firstBlankLine === -1 ? lines : lines.slice(0, firstBlankLine);

  for (const line of headerLines) {
    const match = line.match(REFERRAL_PATTERN);
    if (!match) continue;
    const referral = parseReferralValue(match[1]);
    if (referral) return referral;
  }

  return null;
}

function parseReferralValue(value: string): ParsedReferral | null {
  if (value.length === 0 || /[\r\n\0]/.test(value)) return null;

  let target = value;
  if (/^[a-z][a-z\d+.-]*:\/\//i.test(target)) {
    let url: URL;
    try {
      url = new URL(target);
    } catch {
      return null;
    }

    if (
      url.protocol.toLowerCase() !== "whois:" ||
      url.username !== "" ||
      url.password !== "" ||
      url.search !== "" ||
      url.hash !== "" ||
      !["", "/"].includes(url.pathname)
    ) {
      return null;
    }

    target = url.port ? `[${url.hostname}]:${url.port}` : url.hostname;
  }

  const hostPort = splitHostPort(target);
  if (!hostPort) return null;
  if (hostPort.port !== null && hostPort.port !== "43") return null;

  const hostname = normalizeHostname(hostPort.host);
  if (!hostname || isIP(hostname) !== 0) return null;

  return { hostname, port: 43 };
}

function splitHostPort(
  target: string,
): { host: string; port: string | null } | null {
  if (target.startsWith("[")) {
    const closingBracket = target.indexOf("]");
    if (closingBracket < 0) return null;

    const host = target.slice(1, closingBracket);
    const suffix = target.slice(closingBracket + 1);
    if (suffix === "") return { host, port: null };
    if (!suffix.startsWith(":")) return null;
    return { host, port: suffix.slice(1) };
  }

  if (target.includes(":")) {
    const lastColon = target.lastIndexOf(":");
    return {
      host: target.slice(0, lastColon),
      port: target.slice(lastColon + 1),
    };
  }

  return { host: target, port: null };
}

function normalizeHostname(host: string): string | null {
  if (
    host.length === 0 ||
    host.includes("@") ||
    host.includes("/") ||
    host.includes("[") ||
    host.includes("]")
  ) {
    return null;
  }

  let hostname: string;
  try {
    hostname = domainToASCII(host);
  } catch {
    return null;
  }

  if (
    hostname.length === 0 ||
    hostname.length > 253 ||
    Buffer.byteLength(hostname, "ascii") !== hostname.length ||
    hostname.includes("..")
  ) {
    return null;
  }

  hostname = hostname.replace(/\.$/, "").toLowerCase();
  const labels = hostname.split(".");
  const tld = labels.at(-1);
  const dottedDecimal = labels.every((label) => /^\d+$/.test(label));
  const bareNumericName =
    labels.length === 1 &&
    (/^0[xX][0-9a-fA-F]+$/.test(host) || /^0[0-7]+$/.test(host));

  if (
    dottedDecimal ||
    bareNumericName ||
    !tld ||
    !/[a-z]/.test(tld) ||
    labels.some(
      (label) =>
        label.length === 0 ||
        label.length > 63 ||
        label.startsWith("-") ||
        label.endsWith("-") ||
        !/^[a-z0-9_]+(?:-*[a-z0-9_])*$/i.test(label),
    )
  ) {
    return null;
  }

  return hostname;
}

const defaultLookup: DnsLookup = async (hostname) => {
  const { lookup } = await import("node:dns/promises");
  const records = await lookup(hostname, { all: true });
  return records.map((record) => ({
    address: record.address,
    family: record.family === 4 ? 4 : 6,
  }));
};

export async function resolveReferral(
  referral: ParsedReferral,
  lookup: DnsLookup = defaultLookup,
): Promise<ResolvedReferral> {
  const records = await lookup(referral.hostname);
  const allRecordsAreGlobalUnicast =
    records.length > 0 &&
    records.every((record) => {
      const actualFamily = isIP(record.address);
      return (
        actualFamily === record.family &&
        isGlobalUnicast(record.address, actualFamily) !== null
      );
    });

  if (!allRecordsAreGlobalUnicast) {
    throw new ReferralError(
      `DNS for ${referral.hostname} returned a non-global-unicast address.`,
      "NO_GLOBAL_UNICAST_ADDRESS",
    );
  }

  const selected = records[0];
  if (!selected) {
    throw new ReferralError(
      `No address was resolved for ${referral.hostname}.`,
      "NO_GLOBAL_UNICAST_ADDRESS",
    );
  }

  return {
    ...referral,
    address: selected.address,
    family: selected.family,
  };
}

export function isGlobalUnicast(
  address: string,
  family?: 4 | 6,
): 4 | 6 | null {
  const detectedFamily = family ?? (isIP(address) as 0 | 4 | 6);
  if (detectedFamily !== 4 && detectedFamily !== 6) return null;

  return detectedFamily === 4
    ? isGlobalUnicastIpv4(address)
      ? 4
      : null
    : isGlobalUnicastIpv6(address)
      ? 6
      : null;
}

function isGlobalUnicastIpv4(address: string): boolean {
  const parts = address.split(".").map((part) => Number(part));
  if (
    parts.length !== 4 ||
    parts.some((part) => !Number.isInteger(part) || part < 0 || part > 255)
  ) {
    return false;
  }

  const [first, second] = parts;
  return !(
    first === 0 ||
    first === 10 ||
    first === 127 ||
    (first === 100 && second >= 64 && second <= 127) ||
    (first === 169 && second === 254) ||
    (first === 172 && second >= 16 && second <= 31) ||
    (first === 192 && second === 0) ||
    (first === 192 && second === 168) ||
    (first === 198 && (second === 18 || second === 19)) ||
    (first === 198 && second === 51 && parts[2] === 100) ||
    (first === 203 && second === 0 && parts[2] === 113) ||
    first >= 224
  );
}

function isGlobalUnicastIpv6(address: string): boolean {
  const valueBits = ipv6ToBits(address);
  if (valueBits === null) return false;

  if (inCidr(valueBits, [0xffff, 0, 0, 0, 0, 0], 96)) {
    const value = ipv6ToHex(address);
    if (value === null) return false;
    const embedded = ipv4FromMapped(value);
    return embedded !== null && isGlobalUnicastIpv4(embedded);
  }

  const blockedRanges: ReadonlyArray<readonly [readonly number[], number]> = [
    [[0], 96],
    [[0, 0, 0, 0, 0, 0, 0, 1], 128],
    [[0x0100], 64],
    [[0x2001], 32],
    [[0x2001, 0x0001], 32],
    [[0x2001, 0x0010], 28],
    [[0x2001, 0x0020], 28],
    [[0x2001, 0x0003], 32],
    [[0x2001, 0x0004, 0x0112], 48],
    [[0x2001, 0x0db8], 32],
    [[0x2002], 32],
    [[0x0064, 0xff9b], 96],
    [[0x0064, 0xff9b, 0x0001], 48],
    [[0x2620, 0x004f, 0x0080], 32],
    [[0xfc00], 7],
    [[0xfe80], 10],
    [[0xfec0], 10],
    [[0xff00], 8],
    [[0x3ffe], 16],
  ];

  return !blockedRanges.some(([groups, bits]) =>
    inCidr(valueBits, groups, bits),
  );
}

function ipv6ToHex(address: string): string | null {
  if (isIP(address) !== 6) return null;

  const [addressWithoutZone, zoneIndex] = address.split("%");
  if (zoneIndex !== undefined) return null;

  const dottedMatch = addressWithoutZone.match(
    /^(.*?)(\d+\.\d+\.\d+\.\d+)$/,
  );
  const head = dottedMatch
    ? `${dottedMatch[1].replace(/:$/, "")}:${ipv4ToEmbeddedGroups(dottedMatch[2])}`
    : addressWithoutZone;
  const sides = head.split("::");
  if (sides.length > 2) return null;

  const groups = sides.map((side) =>
    side === "" ? [] : side.split(":").map((group) => parseInt(group, 16)),
  );
  if (groups.some((side) => side.some((group) => Number.isNaN(group)))) {
    return null;
  }

  const missingGroups = 8 - groups[0].length - groups[1].length;
  const fullGroups =
    sides.length === 2
      ? [...groups[0], ...Array(missingGroups).fill(0), ...groups[1]]
      : groups[0];

  if (fullGroups.length !== 8 || fullGroups.some((group) => group > 0xffff)) {
    return null;
  }

  return ipv6GroupsToHex(fullGroups);
}

function ipv4ToEmbeddedGroups(address: string): string {
  const parts = address.split(".").map((part) => Number(part));
  if (
    parts.length !== 4 ||
    parts.some((part) => !Number.isInteger(part) || part < 0 || part > 255)
  ) {
    return "";
  }

  return `${parts[0] * 256 + parts[1]}:${parts[2] * 256 + parts[3]}`;
}

function ipv6GroupsToHex(groups: readonly number[]): string {
  const fullGroups = [...groups, ...Array(8 - groups.length).fill(0)];
  return fullGroups
    .map((group) => group.toString(16).padStart(4, "0"))
    .join("");
}

function inCidr(
  valueBits: string,
  prefixGroups: readonly number[],
  bits: number,
): boolean {
  const prefixBits =
    prefixGroups
      .map((group) => group.toString(2).padStart(16, "0"))
      .join("") + "0".repeat((8 - prefixGroups.length) * 16);
  return valueBits.slice(0, bits) === prefixBits.slice(0, bits);
}

function ipv6ToBits(address: string): string | null {
  const hex = ipv6ToHex(address);
  if (hex === null) return null;
  return hexToBits(hex);
}

function hexToBits(hex: string): string {
  return hex
    .split("")
    .map((nibble) => Number.parseInt(nibble, 16).toString(2).padStart(4, "0"))
    .join("");
}

function ipv4FromMapped(value: string): string | null {
  const embeddedHex = value.match(/([0-9a-f]{4})([0-9a-f]{4})$/i);
  if (!embeddedHex) return null;

  const numeric = Number.parseInt(embeddedHex[1] + embeddedHex[2], 16);
  if (Number.isNaN(numeric)) return null;

  return [
    (numeric >>> 24) & 0xff,
    (numeric >>> 16) & 0xff,
    (numeric >>> 8) & 0xff,
    numeric & 0xff,
  ].join(".");
}

export function createReferralSocket(
  resolved: ResolvedReferral,
  options: ReferralSocketOptions = {},
  factory: ReferralSocketFactory = (socketOptions) => connect(socketOptions),
): Socket {
  const addressFamily = isGlobalUnicast(resolved.address);
  const hostname = normalizeHostname(resolved.hostname);
  if (
    resolved.port !== 43 ||
    hostname === null ||
    addressFamily === null ||
    addressFamily !== resolved.family
  ) {
    throw new ReferralError(
      "Referral socket requires a validated hostname and global unicast address on port 43.",
      "INVALID_REFERRAL",
    );
  }

  const { timeout, ...connectOptions } = options;
  const socket = factory({
    ...connectOptions,
    host: resolved.address,
    family: resolved.family,
    port: 43,
  });

  if (timeout !== undefined) {
    socket.setTimeout(timeout);
  }

  return socket;
}

export async function queryReferral(
  resolved: ResolvedReferral,
  query: string,
  options: FollowOptions = {},
): Promise<string> {
  validateQuery(query);

  return new Promise((resolve, reject) => {
    const socket = createReferralSocket(
      resolved,
      options.socketOptions,
      options.createSocket,
    );
    const chunks: Buffer[] = [];

    socket.on("error", reject);
    socket.on("timeout", () => {
      socket.destroy(
        new ReferralError("Whois connection timed out.", "INVALID_REFERRAL"),
      );
    });
    socket.on("data", (chunk: Buffer) => chunks.push(chunk));
    socket.on("end", () => resolve(Buffer.concat(chunks).toString("utf8")));
    socket.end(`${query}\r\n`);
  });
}

export class ReferralChain {
  readonly #visited = new Set<string>();
  #referralCount = 0;

  constructor(initialHostname?: string) {
    if (initialHostname) {
      this.#visited.add(
        normalizeHostname(initialHostname) ?? initialHostname.toLowerCase(),
      );
    }
  }

  get visitedHosts(): string[] {
    return [...this.#visited];
  }

  get referralCount(): number {
    return this.#referralCount;
  }

  async follow(
    response: string,
    query: string,
    options: FollowOptions = {},
  ): Promise<{ referral: ResolvedReferral; response: string } | null> {
    validateQuery(query);

    if (this.#referralCount >= MAX_REFERRALS) {
      throw new ReferralError(
        "Whois referral limit of three hosts was reached.",
        "REFERRAL_LIMIT",
      );
    }

    const parsed = parseReferral(response);
    if (!parsed) return null;

    if (this.#visited.has(parsed.hostname)) {
      throw new ReferralError(
        `Whois referral returned to visited host ${parsed.hostname}.`,
        "REFERRAL_HOST_REVISITED",
      );
    }

    const resolved = await resolveReferral(parsed, options.lookup);
    const nextResponse = await queryReferral(resolved, query, options);

    this.#visited.add(parsed.hostname);
    this.#referralCount += 1;

    return { referral: resolved, response: nextResponse };
  }
}
