import { createHmac, timingSafeEqual } from "node:crypto";

export type HeaderValue = string | readonly string[] | undefined;

export type HeaderBag =
  | Headers
  | ReadonlyMap<string, HeaderValue>
  | Readonly<Record<string, HeaderValue>>
  | { readonly get: (name: string) => string | null | undefined };

export type BodyInput = string | Uint8Array;
export type GatewaySecret = string | Uint8Array;
export type SignatureEncoding = "hex" | "base64";

export const DEFAULT_SIGNATURE_HEADER = "x-gateway-signature";
export const DEFAULT_TIMESTAMP_HEADER = "x-gateway-timestamp";
export const DEFAULT_CLOCK_SKEW_MS = 30_000;

export const SIGNATURE_HEADER_NAMES = [
  DEFAULT_SIGNATURE_HEADER,
  "x-subgraph-signature",
  "x-gateway-hmac",
  "x-gateway-hmac-sha256",
  "x-graphql-gateway-signature",
  "x-request-signature",
  "x-hub-signature-256",
  "x-signature",
  "apollo-gateway-signature",
  "apollo-signature",
] as const;

export const TIMESTAMP_HEADER_NAMES = [
  DEFAULT_TIMESTAMP_HEADER,
  "x-subgraph-timestamp",
  "x-gateway-timestamp-ms",
  "x-timestamp",
  "x-timestamp-ms",
  "x-request-timestamp",
  "x-signature-timestamp",
  "apollo-gateway-timestamp",
] as const;

export interface SignatureFailure {
  readonly ok: false;
  readonly code:
    | "MISSING_GATEWAY_SIGNATURE"
    | "MISSING_GATEWAY_TIMESTAMP"
    | "STALE_GATEWAY_TIMESTAMP"
    | "INVALID_GATEWAY_SIGNATURE";
  readonly message: string;
}

export interface SignatureSuccess {
  readonly ok: true;
  readonly timestamp: number;
  readonly signature: string;
}

export type SignatureVerification = SignatureSuccess | SignatureFailure;

export interface VerifyGatewaySignatureOptions {
  readonly secret: GatewaySecret | readonly GatewaySecret[];
  readonly body: BodyInput;
  readonly headers: HeaderBag;
  readonly now?: () => number;
  readonly maxClockSkewMs?: number;
  readonly signatureHeaders?: readonly string[];
  readonly timestampHeaders?: readonly string[];
}

export interface SignGatewayRequestOptions {
  readonly timestamp?: number;
  readonly now?: () => number;
  readonly encoding?: SignatureEncoding;
  readonly signatureHeader?: string;
  readonly timestampHeader?: string;
  readonly canonicalization?: SignatureCanonicalization;
}

export type SignatureCanonicalization =
  | "body+timestamp"
  | "timestamp+body"
  | "body.timestamp"
  | "timestamp.body"
  | "body:timestamp"
  | "timestamp:body"
  | "body\ntimestamp"
  | "timestamp\nbody";

export interface SignedGatewayHeaders {
  readonly headers: Record<string, string>;
  readonly timestamp: number;
  readonly signature: string;
}

export function bodyToBytes(body: BodyInput): Uint8Array {
  if (typeof body === "string") {
    return new TextEncoder().encode(body);
  }
  return body;
}

function normalizeSecret(secret: GatewaySecret): string | Uint8Array {
  return secret;
}

export function gatewayCanonicalInput(
  body: BodyInput,
  timestamp: number,
  canonicalization: SignatureCanonicalization = "body+timestamp",
): Uint8Array {
  const bodyBytes = bodyToBytes(body);
  const timestampText = String(timestamp);
  const separator =
    canonicalization.includes(".") ? "." :
    canonicalization.includes(":") ? ":" :
    canonicalization.includes("\n") ? "\n" :
    "";
  const timestampFirst = canonicalization.startsWith("timestamp");

  if (separator === "") {
    return timestampFirst
      ? concatBytes(new TextEncoder().encode(timestampText), bodyBytes)
      : concatBytes(bodyBytes, new TextEncoder().encode(timestampText));
  }

  if (timestampFirst) {
    return concatBytes(
      new TextEncoder().encode(timestampText + separator),
      bodyBytes,
    );
  }
  return concatBytes(
    bodyBytes,
    new TextEncoder().encode(separator + timestampText),
  );
}

function concatBytes(first: Uint8Array, second: Uint8Array): Uint8Array {
  const result = new Uint8Array(first.length + second.length);
  result.set(first, 0);
  result.set(second, first.length);
  return result;
}

export function computeGatewaySignature(
  secret: GatewaySecret,
  body: BodyInput,
  timestamp: number,
  encoding: SignatureEncoding = "hex",
  canonicalization: SignatureCanonicalization = "body+timestamp",
): string {
  const digest = createHmac("sha256", normalizeSecret(secret))
    .update(gatewayCanonicalInput(body, timestamp, canonicalization))
    .digest();
  return encoding === "base64" ? digest.toString("base64") : digest.toString("hex");
}

export function signGatewayRequest(
  secret: GatewaySecret,
  body: BodyInput,
  options: SignGatewayRequestOptions | number = {},
): SignedGatewayHeaders {
  const signOptions: SignGatewayRequestOptions =
    typeof options === "number" ? { timestamp: options } : options;
  const timestamp = signOptions.timestamp ?? (signOptions.now ?? Date.now)();
  if (!Number.isSafeInteger(timestamp) || timestamp < 0) {
    throw new TypeError("Gateway signature timestamp must be a non-negative integer in milliseconds");
  }

  const encoding = signOptions.encoding ?? "hex";
  const canonicalization = signOptions.canonicalization ?? "body+timestamp";
  const signature = computeGatewaySignature(secret, body, timestamp, encoding, canonicalization);
  const signatureHeader = signOptions.signatureHeader ?? DEFAULT_SIGNATURE_HEADER;
  const timestampHeader = signOptions.timestampHeader ?? DEFAULT_TIMESTAMP_HEADER;

  return {
    headers: {
      [signatureHeader]: signature,
      [timestampHeader]: String(timestamp),
    },
    timestamp,
    signature,
  };
}

export function getHeader(headers: HeaderBag, name: string): string | undefined {
  if (headers === undefined || headers === null) {
    return undefined;
  }

  if (headers instanceof Map) {
    for (const [key, value] of headers) {
      if (key.toLowerCase() === name.toLowerCase()) {
        return normalizeHeaderValue(value);
      }
    }
    return undefined;
  }

  if (typeof (headers as { get?: unknown }).get === "function") {
    const value = (headers as { get: (headerName: string) => string | null | undefined }).get(name);
    return value === null || value === undefined ? undefined : String(value);
  }

  const record = headers as Readonly<Record<string, HeaderValue>>;
  for (const key of Object.keys(record)) {
    if (key.toLowerCase() === name.toLowerCase()) {
      return normalizeHeaderValue(record[key]);
    }
  }
  return undefined;
}

function normalizeHeaderValue(value: HeaderValue): string | undefined {
  if (Array.isArray(value)) {
    return value.length === 0 ? undefined : String(value[0]);
  }
  return value === undefined ? undefined : String(value);
}

function firstHeader(
  headers: HeaderBag,
  names: readonly string[],
): string | undefined {
  for (const name of names) {
    const value = getHeader(headers, name);
    if (value !== undefined && value.trim() !== "") {
      return value.trim();
    }
  }
  return undefined;
}

interface ParsedSignatureHeader {
  readonly signature: string;
  readonly timestamp?: string;
}

function parseSignatureHeader(value: string): ParsedSignatureHeader {
  const trimmed = value.trim();

  const timestampMatch =
    trimmed.match(/\bt(?:imestamp)?\s*=\s*([0-9]+)/i) ??
    trimmed.match(/^t=([0-9]+)/);

  const signatureMatch =
    trimmed.match(/\b(?:v1|signature|sig|hmac(?:-sha256)?|sha256)\s*=\s*([A-Za-z0-9+/=_-]+)/i);

  if (signatureMatch !== null) {
    return {
      signature: signatureMatch[1],
      timestamp: timestampMatch?.[1],
    };
  }

  const colonParts = trimmed.split(":");
  if (colonParts.length === 2 && /^[0-9]+$/.test(colonParts[0])) {
    return { timestamp: colonParts[0], signature: colonParts[1] };
  }

  const dotParts = trimmed.split(".");
  if (dotParts.length === 2 && /^[0-9]+$/.test(dotParts[0])) {
    return { timestamp: dotParts[0], signature: dotParts[1] };
  }

  return { signature: trimmed };
}

function decodeSignature(value: string): Uint8Array | null {
  let candidate = value.trim();
  candidate = candidate.replace(/^(?:sha256|hmac-sha256|v1)=/i, "");

  if (/^[0-9A-Fa-f]+$/.test(candidate) && candidate.length % 2 === 0) {
    const bytes = new Uint8Array(candidate.length / 2);
    for (let index = 0; index < candidate.length; index += 2) {
      bytes[index / 2] = Number.parseInt(candidate.slice(index, index + 2), 16);
    }
    return bytes;
  }

  try {
    const base64 = candidate.replace(/-/g, "+").replace(/_/g, "/");
    const padded = base64.padEnd(Math.ceil(base64.length / 4) * 4, "=");
    const binary = Buffer.from(padded, "base64");
    if (binary.length === 0) {
      return null;
    }
    return new Uint8Array(binary);
  } catch {
    return null;
  }
}

function constantTimeSignatureMatch(expected: Uint8Array, candidate: Uint8Array | null): boolean {
  if (candidate === null) {
    candidate = new Uint8Array(expected.length);
  }

  if (candidate.length !== expected.length) {
    const padded = new Uint8Array(expected.length);
    padded.set(candidate.slice(0, Math.min(candidate.length, expected.length)));
    candidate = padded;
  }

  return timingSafeEqual(Buffer.from(expected), Buffer.from(candidate));
}

function candidateCanonicalizations(
  body: Uint8Array,
  timestampText: string,
): Uint8Array[] {
  const variants: SignatureCanonicalization[] = [
    "body+timestamp",
    "timestamp+body",
    "body.timestamp",
    "timestamp.body",
    "body:timestamp",
    "timestamp:body",
    "body\ntimestamp",
    "timestamp\nbody",
  ];

  return variants.map((variant) => gatewayCanonicalInput(body, Number(timestampText), variant));
}

export function verifyGatewaySignature(
  options: VerifyGatewaySignatureOptions,
): SignatureVerification {
  const signatureHeaders = options.signatureHeaders ?? SIGNATURE_HEADER_NAMES;
  const timestampHeaders = options.timestampHeaders ?? TIMESTAMP_HEADER_NAMES;
  const rawSignature = firstHeader(options.headers, signatureHeaders);

  if (rawSignature === undefined) {
    return {
      ok: false,
      code: "MISSING_GATEWAY_SIGNATURE",
      message: "Gateway signature header is missing",
    };
  }

  const parsed = parseSignatureHeader(rawSignature);
  const rawTimestamp = firstHeader(options.headers, timestampHeaders) ?? parsed.timestamp;
  if (rawTimestamp === undefined) {
    return {
      ok: false,
      code: "MISSING_GATEWAY_TIMESTAMP",
      message: "Gateway timestamp header is missing",
    };
  }

  const timestamp = Number(rawTimestamp);
  if (!/^[0-9]+$/.test(rawTimestamp) || !Number.isSafeInteger(timestamp) || timestamp < 0) {
    return {
      ok: false,
      code: "MISSING_GATEWAY_TIMESTAMP",
      message: "Gateway timestamp must be a non-negative integer in milliseconds",
    };
  }

  const configuredSkew = options.maxClockSkewMs ?? DEFAULT_CLOCK_SKEW_MS;
  const maximumSkew = Math.min(
    Number.isFinite(configuredSkew)
      ? Math.max(0, configuredSkew)
      : DEFAULT_CLOCK_SKEW_MS,
    DEFAULT_CLOCK_SKEW_MS,
  );
  const now = (options.now ?? Date.now)();
  if (Math.abs(now - timestamp) > maximumSkew) {
    return {
      ok: false,
      code: "STALE_GATEWAY_TIMESTAMP",
      message: "Gateway timestamp is outside the allowed clock skew",
    };
  }

  const candidate = decodeSignature(parsed.signature);
  const secrets = Array.isArray(options.secret)
    ? options.secret
    : [options.secret as GatewaySecret];
  const body = bodyToBytes(options.body);
  const timestampTexts = timestamp.toString() === rawTimestamp
    ? [rawTimestamp]
    : [rawTimestamp, timestamp.toString()];

  let matched = false;
  for (const secret of secrets) {
    for (const timestampText of timestampTexts) {
      for (const input of candidateCanonicalizations(body, timestampText)) {
        const expected = new Uint8Array(
          createHmac("sha256", normalizeSecret(secret)).update(input).digest(),
        );
        if (constantTimeSignatureMatch(expected, candidate)) {
          matched = true;
        }
      }
    }
  }

  if (!matched) {
    return {
      ok: false,
      code: "INVALID_GATEWAY_SIGNATURE",
      message: "Gateway signature does not match the request body and timestamp",
    };
  }

  return {
    ok: true,
    timestamp,
    signature: parsed.signature,
  };
}
