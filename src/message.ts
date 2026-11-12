import { createHash } from "node:crypto";

/**
 * The only fields allowed in a log message, in the only order accepted by the
 * canonical encoder.
 */
export const MESSAGE_KEYS = [
  "previous",
  "sequence",
  "author",
  "timestamp",
  "hash",
  "content",
  "signature",
] as const;

export const SIGNED_MESSAGE_KEYS = [
  "previous",
  "sequence",
  "author",
  "timestamp",
  "hash",
  "content",
] as const;

export type MessageKey = (typeof MESSAGE_KEYS)[number];

export interface Message {
  previous: string | null;
  sequence: number;
  author: string;
  timestamp: number;
  hash: string | null;
  content: unknown;
  signature: string;
}

export interface SignatureVerificationInput {
  /** The parsed message, including its signature. */
  message: Message;
  /** Canonical text of all seven fields. This is the text that is hashed. */
  canonical: string;
  /** Canonical text of the six fields that are signed. */
  signingPayload: string;
  signature: string;
}

/**
 * Signature checking is deliberately injected. Return true only after the
 * signature has been cryptographically verified for `signingPayload`.
 */
export type SignatureVerifier = (
  input: SignatureVerificationInput,
) => boolean;

export type MessageErrorCode =
  | "INVALID_JSON"
  | "INVALID_MESSAGE"
  | "NON_CANONICAL"
  | "INVALID_SIGNATURE";

export class MessageValidationError extends Error {
  readonly code: MessageErrorCode;

  constructor(code: MessageErrorCode, message: string) {
    super(message);
    this.name = "MessageValidationError";
    this.code = code;
  }
}

export interface CanonicalMessage {
  message: Message;
  canonical: string;
  signingPayload: string;
  id: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

/**
 * Reject anything that would disappear or change when the seven known fields
 * are copied into canonical order. In particular, unknown top-level fields
 * are not accepted.
 */
export function assertMessageShape(value: unknown): asserts value is Message {
  if (!isRecord(value)) {
    throw new MessageValidationError(
      "INVALID_MESSAGE",
      "message must be a JSON object",
    );
  }

  const keys = Object.keys(value);
  if (
    keys.length !== MESSAGE_KEYS.length ||
    MESSAGE_KEYS.some((key) => !Object.prototype.hasOwnProperty.call(value, key))
  ) {
    throw new MessageValidationError(
      "INVALID_MESSAGE",
      `message must contain exactly these fields: ${MESSAGE_KEYS.join(", ")}`,
    );
  }

  if (!(value.previous === null || typeof value.previous === "string")) {
    throw new MessageValidationError(
      "INVALID_MESSAGE",
      "previous must be a string or null",
    );
  }

  if (
    typeof value.sequence !== "number" ||
    !Number.isSafeInteger(value.sequence) ||
    value.sequence < 1
  ) {
    throw new MessageValidationError(
      "INVALID_MESSAGE",
      "sequence must be a positive safe integer",
    );
  }

  if (typeof value.author !== "string" || value.author.length === 0) {
    throw new MessageValidationError(
      "INVALID_MESSAGE",
      "author must be a non-empty string",
    );
  }

  if (!isFiniteNumber(value.timestamp)) {
    throw new MessageValidationError(
      "INVALID_MESSAGE",
      "timestamp must be a finite number",
    );
  }

  if (!(value.hash === null || typeof value.hash === "string")) {
    throw new MessageValidationError(
      "INVALID_MESSAGE",
      "hash must be a string or null",
    );
  }

  if (typeof value.signature !== "string" || value.signature.length === 0) {
    throw new MessageValidationError(
      "INVALID_MESSAGE",
      "signature must be a non-empty string",
    );
  }
}

/**
 * Serialize the full message, including signature, in the fixed seven-field
 * order and with two-space indentation.
 */
export function serializeCanonical(message: Message): string {
  assertMessageShape(message);

  return JSON.stringify(
    {
      previous: message.previous,
      sequence: message.sequence,
      author: message.author,
      timestamp: message.timestamp,
      hash: message.hash,
      content: message.content,
      signature: message.signature,
    },
    null,
    2,
  );
}

/**
 * Serialize the portion covered by the author's signature. As in SSB, the
 * signature field itself is not part of the signed payload.
 */
export function serializeSigningPayload(message: Message): string {
  assertMessageShape(message);

  return JSON.stringify(
    {
      previous: message.previous,
      sequence: message.sequence,
      author: message.author,
      timestamp: message.timestamp,
      hash: message.hash,
      content: message.content,
    },
    null,
    2,
  );
}

export function computeIdFromCanonicalText(canonical: string): string {
  const digest = createHash("sha256").update(canonical, "utf8").digest("base64");
  return `${digest}.sha256`;
}

/**
 * Message ID: Base64(SHA-256(UTF-8 bytes of full canonical text)) plus
 * ".sha256". The signature is included in the hashed text.
 */
export function computeMessageId(message: Message): string {
  return computeIdFromCanonicalText(serializeCanonical(message));
}

export function parseMessageText(text: string): Message {
  if (typeof text !== "string") {
    throw new MessageValidationError(
      "INVALID_JSON",
      "message input must be a string",
    );
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error);
    throw new MessageValidationError(
      "INVALID_JSON",
      `message is not valid JSON: ${detail}`,
    );
  }

  assertMessageShape(parsed);
  return parsed;
}

/**
 * Parse the exact wire text, reject it unless it already equals the canonical
 * form, then verify the injected signature and calculate its ID.
 */
export function inspectMessageText(
  text: string,
  verify: SignatureVerifier,
): CanonicalMessage {
  if (typeof verify !== "function") {
    throw new TypeError("a signature verifier must be provided");
  }

  const message = parseMessageText(text);
  const canonical = serializeCanonical(message);

  if (text !== canonical) {
    throw new MessageValidationError(
      "NON_CANONICAL",
      "message text is not the canonical serialization",
    );
  }

  const signingPayload = serializeSigningPayload(message);
  const verified = verify({
    message,
    canonical,
    signingPayload,
    signature: message.signature,
  });

  if (verified !== true) {
    throw new MessageValidationError(
      "INVALID_SIGNATURE",
      "message signature verification failed",
    );
  }

  return {
    message,
    canonical,
    signingPayload,
    id: computeIdFromCanonicalText(canonical),
  };
}

/**
 * Canonicalize a parsed message object, or require an input string to already
 * be canonical. This does not verify a signature or a log chain.
 */
export function canonicalizeMessage(input: string | Message): CanonicalMessage {
  if (typeof input === "string") {
    const message = parseMessageText(input);
    const canonical = serializeCanonical(message);

    if (input !== canonical) {
      throw new MessageValidationError(
        "NON_CANONICAL",
        "message text is not the canonical serialization",
      );
    }

    return {
      message,
      canonical,
      signingPayload: serializeSigningPayload(message),
      id: computeIdFromCanonicalText(canonical),
    };
  }

  assertMessageShape(input);
  const canonical = serializeCanonical(input);
  return {
    message: input,
    canonical,
    signingPayload: serializeSigningPayload(input),
    id: computeIdFromCanonicalText(canonical),
  };
}

export function isCanonicalMessageText(text: string): boolean {
  try {
    const message = parseMessageText(text);
    return serializeCanonical(message) === text;
  } catch {
    return false;
  }
}
