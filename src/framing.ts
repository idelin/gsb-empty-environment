import { BoundaryError, fail } from "./errors.ts";
import {
  connectionTokens,
  headerValues,
  isToken,
  splitCommaList,
  trimOws,
} from "./headers.ts";
import type {
  BoundaryPolicy,
  FramingDecision,
  HeaderEntry,
  ParsedRequestHead,
  ResolvedBoundaryPolicy,
  UpgradeDecision,
} from "./types.ts";

const DEFAULTS = {
  maxHeaderBytes: 16 * 1024,
  maxContentLength: 64 * 1024 * 1024,
  maxChunkSize: 64 * 1024 * 1024,
  allowTrailers: false,
  allowH2cDowngrade: false,
  allowGenericUpgrade: false,
  allowPipelining: false,
} as const;

const HOP_BY_HOP = new Set([
  "connection",
  "proxy-connection",
  "keep-alive",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "http2-settings",
]);

const PROTOCOL = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+(?:\/[!#$%&'*+\-.^_`|~0-9A-Za-z]+)?$/;
const H2C_SETTINGS = /^[A-Za-z0-9_-]+={0,2}$/;

export function resolvePolicy(
  policy: BoundaryPolicy = {},
): ResolvedBoundaryPolicy {
  const maxContentLength =
    policy.maxContentLength ?? DEFAULTS.maxContentLength;
  const maxChunkSize = policy.maxChunkSize ?? maxContentLength;

  for (const [name, value] of [
    ["maxHeaderBytes", policy.maxHeaderBytes ?? DEFAULTS.maxHeaderBytes],
    ["maxContentLength", maxContentLength],
    ["maxChunkSize", maxChunkSize],
  ] as const) {
    if (!Number.isSafeInteger(value) || value < 0) {
      throw new RangeError(`${name} must be a non-negative safe integer`);
    }
  }

  return {
    maxHeaderBytes: policy.maxHeaderBytes ?? DEFAULTS.maxHeaderBytes,
    maxContentLength,
    maxChunkSize,
    allowTrailers: policy.allowTrailers ?? DEFAULTS.allowTrailers,
    allowH2cDowngrade:
      policy.allowH2cDowngrade ?? DEFAULTS.allowH2cDowngrade,
    allowGenericUpgrade:
      policy.allowGenericUpgrade ?? DEFAULTS.allowGenericUpgrade,
    allowPipelining: policy.allowPipelining ?? DEFAULTS.allowPipelining,
  };
}

/**
 * Decide one framing for the request. Every ambiguity that could lead two
 * parsers to different body boundaries is rejected here.
 */
export function decideRequestFraming(
  head: ParsedRequestHead,
  policy: ResolvedBoundaryPolicy,
): FramingDecision {
  const contentLengths = headerValues(head.headers, "content-length");
  const transferEncodings = headerValues(head.headers, "transfer-encoding");
  const upgrade = parseUpgrade(head, contentLengths, transferEncodings, policy);

  if (upgrade !== null && !upgrade.h2cDowngraded && upgrade.protocol !== "h2c") {
    if (contentLengths.length > 0 || transferEncodings.length > 0) {
      fail(
        "UPGRADE_WITH_BODY",
        "generic Upgrade requests cannot carry HTTP/1 body framing",
      );
    }
    return {
      kind: "upgrade",
      contentLength: null,
      transferCodings: [],
      upgrade,
      normalizedHeaders: buildNormalizedHeaders(head.headers, upgrade),
    };
  }

  if (contentLengths.length > 0 && transferEncodings.length > 0) {
    fail(
      "BOTH_CONTENT_LENGTH_AND_TRANSFER_ENCODING",
      "request declares both Content-Length and Transfer-Encoding",
    );
  }

  if (contentLengths.length > 1) {
    fail("DUPLICATE_CONTENT_LENGTH", "multiple Content-Length header fields");
  }

  if (contentLengths.length === 1) {
    const contentLength = parseContentLength(contentLengths[0], policy);
    return {
      kind: "content-length",
      contentLength,
      transferCodings: [],
      upgrade,
      normalizedHeaders: buildNormalizedHeaders(head.headers, upgrade),
    };
  }

  if (transferEncodings.length > 0) {
    const transferCodings = parseTransferEncoding(transferEncodings, policy);
    return {
      kind: "chunked",
      contentLength: null,
      transferCodings,
      upgrade,
      normalizedHeaders: buildNormalizedHeaders(head.headers, upgrade),
    };
  }

  return {
    kind: "none",
    contentLength: 0,
    transferCodings: [],
    upgrade,
    normalizedHeaders: buildNormalizedHeaders(head.headers, upgrade),
  };
}

export function finalizeHeaders(
  decision: FramingDecision,
  decodedBodyLength: number,
): ReadonlyArray<HeaderEntry> {
  const headers: HeaderEntry[] = [...decision.normalizedHeaders];

  if (decision.kind === "content-length" || decision.kind === "chunked") {
    headers.push(["content-length", String(decodedBodyLength)]);
  }

  return Object.freeze(headers.map(([name, value]) => [name, value] as const));
}

function parseContentLength(
  value: string,
  policy: ResolvedBoundaryPolicy,
): number {
  if (!/^[0-9]+$/.test(value)) {
    fail(
      "INVALID_CONTENT_LENGTH",
      "Content-Length must be a non-empty decimal integer without a sign",
    );
  }

  let length: bigint;
  try {
    length = BigInt(value);
  } catch {
    fail("INVALID_CONTENT_LENGTH", "invalid Content-Length integer");
  }

  if (length > BigInt(Number.MAX_SAFE_INTEGER)) {
    fail(
      "INVALID_CONTENT_LENGTH",
      "Content-Length exceeds safe integer range",
    );
  }
  if (length > BigInt(policy.maxContentLength)) {
    fail(
      "CONTENT_LENGTH_TOO_LARGE",
      `Content-Length exceeds ${policy.maxContentLength}`,
    );
  }

  return Number(length);
}

function parseTransferEncoding(
  values: ReadonlyArray<string>,
  policy: ResolvedBoundaryPolicy,
): ReadonlyArray<string> {
  void policy;

  if (values.length > 1) {
    fail(
      "DUPLICATE_TRANSFER_ENCODING",
      "multiple Transfer-Encoding header fields are ambiguous",
    );
  }

  const codings: string[] = [];
  const seen = new Set<string>();
  for (const item of splitCommaList(values[0])) {
    if (item.includes(";")) {
      fail(
        "UNSUPPORTED_TRANSFER_CODING",
        "transfer-coding parameters are not supported",
      );
    }
    if (!isToken(item)) {
      fail("INVALID_TRANSFER_ENCODING", "invalid transfer-coding token");
    }
    const coding = item.toLowerCase();
    if (seen.has(coding)) {
      fail(
        "DUPLICATE_TRANSFER_ENCODING",
        `duplicate transfer-coding ${coding}`,
      );
    }
    seen.add(coding);
    codings.push(coding);
  }

  const finalCoding = codings[codings.length - 1];
  if (finalCoding !== "chunked") {
    fail(
      "TRANSFER_ENCODING_NOT_CHUNKED",
      `final transfer-coding is ${finalCoding}, not chunked`,
    );
  }
  if (codings.length !== 1) {
    fail(
      "UNSUPPORTED_TRANSFER_CODING",
      "only a single chunked transfer-coding can be forwarded canonically",
    );
  }

  return codings;
}

function parseUpgrade(
  head: ParsedRequestHead,
  contentLengths: ReadonlyArray<string>,
  transferEncodings: ReadonlyArray<string>,
  policy: ResolvedBoundaryPolicy,
): UpgradeDecision | null {
  const upgradeValues = headerValues(head.headers, "upgrade");
  const connection = connectionTokens(head.headers);
  const settingsValues = headerValues(head.headers, "http2-settings");

  if (upgradeValues.length === 0) {
    if (connection.has("upgrade")) {
      fail(
        "INVALID_UPGRADE",
        "Connection nominates Upgrade but no Upgrade header is present",
      );
    }
    if (settingsValues.length > 0) {
      fail(
        "INVALID_UPGRADE",
        "HTTP2-Settings is present without an h2c Upgrade",
      );
    }
    return null;
  }
  if (head.version !== "HTTP/1.1") {
    fail("INVALID_UPGRADE", "Upgrade requires HTTP/1.1");
  }
  if (upgradeValues.length !== 1) {
    fail("INVALID_UPGRADE", "multiple Upgrade header fields are ambiguous");
  }
  if (!connection.has("upgrade")) {
    fail(
      "INVALID_UPGRADE",
      "Upgrade header requires Connection: upgrade",
    );
  }

  const protocols = splitCommaList(upgradeValues[0]);
  if (protocols.length !== 1 || !PROTOCOL.test(protocols[0])) {
    fail("INVALID_UPGRADE", "exactly one valid Upgrade protocol is required");
  }
  const protocol = protocols[0].toLowerCase();

  if (protocol === "h2c") {
    if (settingsValues.length === 0) {
      fail("H2C_SETTINGS_MISSING", "h2c Upgrade requires HTTP2-Settings");
    }
    if (settingsValues.length !== 1) {
      fail(
        "INVALID_H2C_SETTINGS",
        "multiple HTTP2-Settings header fields are ambiguous",
      );
    }
    const settings = trimOws(settingsValues[0]);
    if (
      settings.length === 0 ||
      settings.length > 4096 ||
      !H2C_SETTINGS.test(settings)
    ) {
      fail("INVALID_H2C_SETTINGS", "HTTP2-Settings is not base64url");
    }
    if (!policy.allowH2cDowngrade) {
      fail(
        "UPGRADE_NOT_ALLOWED",
        "h2c Upgrade is disabled; set allowH2cDowngrade explicitly",
      );
    }
    void contentLengths;
    void transferEncodings;
    return {
      protocol: "h2c",
      h2cDowngraded: true,
      h2cSettings: settings,
    };
  }

  if (!policy.allowGenericUpgrade) {
    fail(
      "UPGRADE_NOT_ALLOWED",
      `Upgrade protocol ${protocol} is disabled by policy`,
    );
  }

  return {
    protocol,
    h2cDowngraded: false,
  };
}

function buildNormalizedHeaders(
  headers: ReadonlyArray<HeaderEntry>,
  upgrade: UpgradeDecision | null,
): ReadonlyArray<HeaderEntry> {
  const connection = connectionTokens(headers);
  const remove = new Set<string>(HOP_BY_HOP);
  for (const token of connection) {
    remove.add(token);
  }

  const keepUpgrade =
    upgrade !== null &&
    !upgrade.h2cDowngraded &&
    upgrade.protocol !== "h2c";

  const normalized: HeaderEntry[] = [];

  for (const [name, value] of headers) {
    if (name === "content-length" || name === "transfer-encoding") continue;
    if (remove.has(name)) continue;
    normalized.push([name, value]);
  }

  if (keepUpgrade && upgrade !== null) {
    normalized.push(["connection", "upgrade"]);
    normalized.push(["upgrade", upgrade.protocol]);
  }

  return Object.freeze(
    normalized.map(([name, value]) => [name, value] as const),
  );
}

export function isBoundaryError(error: unknown): error is BoundaryError {
  return error instanceof BoundaryError;
}
