import { createHmac, timingSafeEqual } from "node:crypto";
import { InvalidLastEventIdError } from "./errors.ts";

export const MAX_LAST_EVENT_ID_BYTES = 200;
export const SIGNATURE_BYTES = 16;
export const SIGNATURE_HEX_LENGTH = SIGNATURE_BYTES * 2;

export type SseSecret = string | Buffer | Uint8Array;

export interface StreamIdentity {
  /** The stream that the subscription is currently reading. */
  stream: string;
  /** The authenticated tenant that owns the subscription. */
  tenant: string;
}

export interface SignedEventReference extends StreamIdentity {
  /** Zero-based sequence number of the event that was delivered. */
  sequence: number;
}

export interface IssueEventIdOptions extends SignedEventReference {
  secret: SseSecret;
}

export interface ParsedEventId {
  stream: string;
  sequence: number;
  /** Lower-case hexadecimal signature carried by the ID. */
  signature: string;
}

export interface VerifyEventIdOptions extends StreamIdentity {
  secret: SseSecret;
}

function assertSecret(secret: SseSecret): void {
  if (
    typeof secret !== "string" &&
    !Buffer.isBuffer(secret) &&
    !(secret instanceof Uint8Array)
  ) {
    throw new TypeError("secret must be a string, Buffer, or Uint8Array");
  }

  const byteLength =
    typeof secret === "string"
      ? Buffer.byteLength(secret, "utf8")
      : secret.byteLength;
  if (byteLength === 0) {
    throw new TypeError("secret must not be empty");
  }
}

function assertIdentity(identity: StreamIdentity): void {
  if (
    typeof identity.stream !== "string" ||
    identity.stream.length === 0 ||
    /[\r\n\0]/.test(identity.stream)
  ) {
    throw new TypeError("stream must be a non-empty string without CR, LF, or NUL");
  }
  if (
    typeof identity.tenant !== "string" ||
    identity.tenant.length === 0 ||
    /[\r\n\0]/.test(identity.tenant)
  ) {
    throw new TypeError("tenant must be a non-empty string without CR, LF, or NUL");
  }
}

function assertSequence(sequence: number): void {
  if (!Number.isSafeInteger(sequence) || sequence < 0) {
    throw new TypeError("sequence must be a non-negative safe integer");
  }
}

/**
 * The tenant and stream are serialized as a JSON tuple so delimiter characters
 * in either value cannot make two different bindings share an HMAC input.
 */
function signatureInput(identity: StreamIdentity, sequence: number): Buffer {
  return Buffer.from(
    JSON.stringify([identity.tenant, identity.stream, sequence]),
    "utf8",
  );
}

function computeSignature(
  identity: StreamIdentity,
  sequence: number,
  secret: SseSecret,
): string {
  assertSecret(secret);
  assertIdentity(identity);
  assertSequence(sequence);

  return createHmac("sha256", secret)
    .update(signatureInput(identity, sequence))
    .digest("hex")
    .slice(0, SIGNATURE_HEX_LENGTH);
}

function assertIdTransportSafe(raw: string): void {
  if (Buffer.byteLength(raw, "utf8") > MAX_LAST_EVENT_ID_BYTES) {
    throw new InvalidLastEventIdError();
  }
  if (/[\r\n\0]/.test(raw)) {
    throw new InvalidLastEventIdError();
  }
}

export function issueEventId(options: IssueEventIdOptions): string;
export function issueEventId(
  stream: string,
  sequence: number,
  tenant: string,
  secret: SseSecret,
): string;
export function issueEventId(
  streamOrOptions: string | IssueEventIdOptions,
  sequence?: number,
  tenant?: string,
  secret?: SseSecret,
): string {
  const options: IssueEventIdOptions =
    typeof streamOrOptions === "string"
      ? {
          stream: streamOrOptions,
          sequence: sequence as number,
          tenant: tenant as string,
          secret: secret as SseSecret,
        }
      : streamOrOptions;

  const signature = computeSignature(options, options.sequence, options.secret);
  const id = `${options.stream}:${options.sequence}:${signature}`;

  if (Buffer.byteLength(id, "utf8") > MAX_LAST_EVENT_ID_BYTES) {
    throw new TypeError(
      `event ID exceeds ${MAX_LAST_EVENT_ID_BYTES} UTF-8 bytes`,
    );
  }
  return id;
}

export const signEventId = issueEventId;

/**
 * Parses the transport syntax only. Signature verification is deliberately a
 * separate step so a well-formed but forged cursor can fail closed with a reset.
 */
export function parseEventId(raw: string): ParsedEventId {
  if (typeof raw !== "string") {
    throw new InvalidLastEventIdError();
  }
  assertIdTransportSafe(raw);

  const signatureColon = raw.lastIndexOf(":");
  const sequenceColon = raw.lastIndexOf(":", signatureColon - 1);
  if (signatureColon <= 0 || sequenceColon <= 0) {
    throw new InvalidLastEventIdError();
  }

  const stream = raw.slice(0, sequenceColon);
  const sequenceText = raw.slice(sequenceColon + 1, signatureColon);
  const signature = raw.slice(signatureColon + 1);

  if (
    stream.length === 0 ||
    !/^(0|[1-9][0-9]*)$/.test(sequenceText) ||
    signature.length === 0
  ) {
    throw new InvalidLastEventIdError();
  }

  const sequence = Number(sequenceText);
  if (!Number.isSafeInteger(sequence)) {
    throw new InvalidLastEventIdError();
  }

  return { stream, sequence, signature: signature.toLowerCase() };
}

function signatureMatches(expectedHex: string, actualHex: string): boolean {
  if (!new RegExp(`^[0-9a-f]{${SIGNATURE_HEX_LENGTH}}$`).test(actualHex)) {
    return false;
  }

  const expected = Buffer.from(expectedHex, "hex");
  const actual = Buffer.from(actualHex, "hex");
  return expected.length === actual.length && timingSafeEqual(expected, actual);
}

export function verifyEventId(
  eventId: string,
  options: VerifyEventIdOptions,
): boolean;
export function verifyEventId(
  eventId: string,
  identity: StreamIdentity,
  secret: SseSecret,
): boolean;
export function verifyEventId(
  eventId: string,
  optionsOrIdentity: VerifyEventIdOptions | StreamIdentity,
  maybeSecret?: SseSecret,
): boolean {
  let parsed: ParsedEventId;
  try {
    parsed = parseEventId(eventId);
  } catch {
    return false;
  }

  const options: VerifyEventIdOptions =
    maybeSecret === undefined
      ? (optionsOrIdentity as VerifyEventIdOptions)
      : { ...(optionsOrIdentity as StreamIdentity), secret: maybeSecret };

  let expected: string;
  try {
    expected = computeSignature(options, parsed.sequence, options.secret);
  } catch {
    return false;
  }

  const signatureOk = signatureMatches(expected, parsed.signature);
  return signatureOk && parsed.stream === options.stream;
}
