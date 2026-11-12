import { createHash } from "node:crypto";

const UTF8 = new TextEncoder();
const EMPTY = new Uint8Array(0);

export type BytesLike = Uint8Array | string;
export type HashFunction = (input: Uint8Array) => Uint8Array;
export type ISKHash = string | HashFunction;

/**
 * A complete CPace message. Callers that already have the network encoding can
 * pass a byte string directly; otherwise the public share and associated data
 * are encoded separately, as required by the CPace transcript functions.
 */
export interface HandshakeMessage {
  publicShare: BytesLike;
  associatedData?: BytesLike;
}

export type MessageInput = BytesLike | HandshakeMessage;

export interface ISKBaseInput {
  /**
   * The complete domain-separation string for ISK derivation. For a CPace
   * cipher suite this is normally `G.DSI || "_ISK"`.
   */
  domainSeparator: BytesLike;
  sid: BytesLike;
  sharedPoint: BytesLike;
  /**
   * The group's encoded neutral element. If omitted, the default check rejects
   * an all-zero encoding, which covers the common CPace groups.
   */
  identityElement?: BytesLike;
  isIdentityElement?: (sharedPoint: Uint8Array) => boolean;
  hash?: ISKHash;
}

export interface SymmetricISKInput extends ISKBaseInput {
  mode: "symmetric";
  messages: readonly [MessageInput, MessageInput];
}

export interface InitiatorResponderISKInput extends ISKBaseInput {
  mode: "initiator-responder";
  initiatorMessage: MessageInput;
  responderMessage: MessageInput;
}

export type DeriveISKInput =
  | SymmetricISKInput
  | InitiatorResponderISKInput;

export type MessageTranscriptInput =
  | {
      mode: "symmetric";
      messages: readonly [MessageInput, MessageInput];
    }
  | {
      mode: "initiator-responder";
      initiatorMessage: MessageInput;
      responderMessage: MessageInput;
    };

export class InvalidInputError extends Error {
  code = "ERR_CPACE_INVALID_INPUT";

  constructor(message: string) {
    super(message);
    this.name = "InvalidInputError";
  }
}

export class IdentityPointError extends Error {
  code = "ERR_CPACE_IDENTITY_POINT";

  constructor(message = "CPace shared point K is the group identity element") {
    super(message);
    this.name = "IdentityPointError";
  }
}

export class SidReuseError extends Error {
  code = "ERR_CPACE_SID_REUSE";

  constructor(message = "CPace sid was already used by a completed session") {
    super(message);
    this.name = "SidReuseError";
  }
}

export function encodeUtf8(value: string): Uint8Array {
  return UTF8.encode(value);
}

export function toBytes(value: BytesLike, field = "value"): Uint8Array {
  if (typeof value === "string") {
    return encodeUtf8(value);
  }
  if (value instanceof Uint8Array) {
    return new Uint8Array(value);
  }
  throw new TypeError(`${field} must be a string or Uint8Array`);
}

export function concatBytes(...chunks: Uint8Array[]): Uint8Array {
  let length = 0;
  for (const chunk of chunks) {
    length += chunk.length;
    if (!Number.isSafeInteger(length)) {
      throw new RangeError("concatenated byte string is too large");
    }
  }

  const result = new Uint8Array(length);
  let offset = 0;
  for (const chunk of chunks) {
    result.set(chunk, offset);
    offset += chunk.length;
  }
  return result;
}

/**
 * Encodes a non-negative integer using canonical LEB128, as used by CPace's
 * prepend_len function.
 */
export function encodeLeb128(value: number): Uint8Array {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new RangeError("LEB128 value must be a non-negative safe integer");
  }

  const encoded: number[] = [];
  do {
    let byte = value % 128;
    value = Math.floor(value / 128);
    if (value !== 0) {
      byte |= 0x80;
    }
    encoded.push(byte);
  } while (value !== 0);

  return Uint8Array.from(encoded);
}

export function prependLength(value: BytesLike): Uint8Array {
  const bytes = toBytes(value);
  return concatBytes(encodeLeb128(bytes.length), bytes);
}

/** CPace's length-value concatenation: prepend_len(a0) || ... || prepend_len(an). */
export function lvCat(...segments: BytesLike[]): Uint8Array {
  const encoded: Uint8Array[] = [];
  for (const segment of segments) {
    encoded.push(prependLength(segment));
  }
  return concatBytes(...encoded);
}

/** Returns -1, 0, or 1 using unsigned byte-wise lexicographic ordering. */
export function compareBytes(left: Uint8Array, right: Uint8Array): -1 | 0 | 1 {
  const commonLength = Math.min(left.length, right.length);
  for (let index = 0; index < commonLength; index += 1) {
    if (left[index] < right[index]) {
      return -1;
    }
    if (left[index] > right[index]) {
      return 1;
    }
  }
  if (left.length < right.length) {
    return -1;
  }
  if (left.length > right.length) {
    return 1;
  }
  return 0;
}

/**
 * CPace o_cat: lexicographically larger input first, prefixed by the ASCII
 * bytes "oc" so symmetric and role-ordered transcripts are domain separated.
 */
export function orderedConcat(left: Uint8Array, right: Uint8Array): Uint8Array {
  const tag = encodeUtf8("oc");
  return compareBytes(left, right) >= 0
    ? concatBytes(tag, left, right)
    : concatBytes(tag, right, left);
}

function isHandshakeMessage(value: MessageInput): value is HandshakeMessage {
  return (
    typeof value === "object" &&
    value !== null &&
    !(value instanceof Uint8Array) &&
    "publicShare" in value
  );
}

function encodeMessage(message: MessageInput): Uint8Array {
  if (isHandshakeMessage(message)) {
    return lvCat(message.publicShare, message.associatedData ?? EMPTY);
  }
  return lvCat(message);
}

/**
 * Encodes only the messages: transcript_oc for symmetric CPace and
 * transcript_ir for the initiator/responder setting.
 */
export function buildMessageTranscript(
  input: MessageTranscriptInput,
): Uint8Array {
  if (input.mode === "symmetric") {
    const messageA = encodeMessage(input.messages[0]);
    const messageB = encodeMessage(input.messages[1]);
    return orderedConcat(messageA, messageB);
  }
  if (input.mode === "initiator-responder") {
    return concatBytes(
      encodeMessage(input.initiatorMessage),
      encodeMessage(input.responderMessage),
    );
  }
  throw new InvalidInputError(`unsupported CPace mode: ${String(input.mode)}`);
}

function defaultIdentityCheck(sharedPoint: Uint8Array): boolean {
  if (sharedPoint.length === 0) {
    return true;
  }
  for (const byte of sharedPoint) {
    if (byte !== 0) {
      return false;
    }
  }
  return true;
}

function assertValidSharedPoint(input: ISKBaseInput): Uint8Array {
  const sharedPoint = toBytes(input.sharedPoint, "sharedPoint");
  if (sharedPoint.length === 0) {
    throw new InvalidInputError("sharedPoint must not be empty");
  }
  if (input.identityElement !== undefined && input.isIdentityElement !== undefined) {
    throw new InvalidInputError(
      "provide either identityElement or isIdentityElement, not both",
    );
  }

  let isIdentity: boolean;
  if (input.identityElement !== undefined) {
    isIdentity =
      compareBytes(sharedPoint, toBytes(input.identityElement, "identityElement")) ===
      0;
  } else if (input.isIdentityElement !== undefined) {
    isIdentity = Boolean(input.isIdentityElement(new Uint8Array(sharedPoint)));
  } else {
    isIdentity = defaultIdentityCheck(sharedPoint);
  }

  if (isIdentity) {
    throw new IdentityPointError();
  }
  return sharedPoint;
}

function buildTranscriptWithPoint(
  input: DeriveISKInput,
  sharedPoint: Uint8Array,
): Uint8Array {
  const domainSeparator = toBytes(input.domainSeparator, "domainSeparator");
  if (domainSeparator.length === 0) {
    throw new InvalidInputError("domainSeparator must not be empty");
  }

  const sid = toBytes(input.sid, "sid");
  const prefix = lvCat(domainSeparator, sid, sharedPoint);

  const messages = buildMessageTranscript(input);

  return concatBytes(prefix, messages);
}

/**
 * Builds the exact byte string that CPace hashes for ISK:
 *
 *   lv_cat(DSI_ISK, sid, K) || transcript(messages)
 *
 * Symmetric messages use o_cat; initiator/responder messages are kept in role
 * order.
 */
export function buildTranscript(input: DeriveISKInput): Uint8Array {
  return buildTranscriptWithPoint(input, assertValidSharedPoint(input));
}

function hashTranscript(transcript: Uint8Array, hash: ISKHash | undefined): Uint8Array {
  if (typeof hash === "function") {
    const digest = hash(new Uint8Array(transcript));
    if (!(digest instanceof Uint8Array)) {
      throw new TypeError("hash function must return a Uint8Array");
    }
    if (digest.length === 0) {
      throw new TypeError("hash function returned an empty digest");
    }
    return new Uint8Array(digest);
  }

  const algorithm = hash ?? "sha512";
  return new Uint8Array(
    createHash(algorithm).update(transcript).digest(),
  );
}

/**
 * Stateless ISK derivation. Use ISKDeriver when a completed-session sid
 * registry is required by the login handshake.
 */
export function deriveISK(input: DeriveISKInput): Uint8Array {
  const transcript = buildTranscript(input);
  return hashTranscript(transcript, input.hash);
}

/** Lower-case camel-case alias for SDKs that use camel-case acronyms. */
export const deriveIsk = deriveISK;

export interface CompletedSidStore {
  hasCompleted(sid: Uint8Array): boolean;
  markCompleted(sid: Uint8Array): void;
}

function bytesToHex(bytes: Uint8Array): string {
  let result = "";
  for (const byte of bytes) {
    result += byte.toString(16).padStart(2, "0");
  }
  return result;
}

/** In-memory completed-session registry. A store should be shared by the SDK instance. */
export class InMemoryCompletedSidStore implements CompletedSidStore {
  #completed = new Set<string>();

  constructor(initialSids: Iterable<BytesLike> = []) {
    for (const sid of initialSids) {
      this.markCompleted(toBytes(sid, "sid"));
    }
  }

  hasCompleted(sid: Uint8Array): boolean {
    return this.#completed.has(bytesToHex(toBytes(sid, "sid")));
  }

  markCompleted(sid: Uint8Array): void {
    this.#completed.add(bytesToHex(toBytes(sid, "sid")));
  }

  clear(): void {
    this.#completed.clear();
  }
}

export interface ISKDeriverOptions {
  completedSidStore?: CompletedSidStore;
  hash?: ISKHash;
  identityElement?: BytesLike;
  isIdentityElement?: (sharedPoint: Uint8Array) => boolean;
}

/**
 * Stateful login-handshake entry point. A successful derive both returns the
 * ISK and permanently marks the sid as completed. Failed derivations (for
 * example an identity K) do not consume the sid.
 */
export class ISKDeriver {
  #completedSidStore: CompletedSidStore;
  #hash: ISKHash | undefined;
  #identityElement: BytesLike | undefined;
  #isIdentityElement: ((sharedPoint: Uint8Array) => boolean) | undefined;

  constructor(options: ISKDeriverOptions = {}) {
    this.#completedSidStore =
      options.completedSidStore ?? new InMemoryCompletedSidStore();
    this.#hash = options.hash;
    this.#identityElement = options.identityElement;
    this.#isIdentityElement = options.isIdentityElement;
  }

  derive(input: DeriveISKInput): Uint8Array {
    const sid = toBytes(input.sid, "sid");
    if (this.#completedSidStore.hasCompleted(sid)) {
      throw new SidReuseError();
    }

    const hasInputIdentityElement = input.identityElement !== undefined;
    const hasInputIdentityCheck = input.isIdentityElement !== undefined;
    const effectiveInput: DeriveISKInput = Object.assign({}, input, {
      hash: input.hash ?? this.#hash,
      identityElement: hasInputIdentityElement
        ? input.identityElement
        : hasInputIdentityCheck
          ? undefined
          : this.#identityElement,
      isIdentityElement: hasInputIdentityCheck
        ? input.isIdentityElement
        : hasInputIdentityElement
          ? undefined
          : this.#isIdentityElement,
    });

    const isk = deriveISK(effectiveInput);
    this.#completedSidStore.markCompleted(sid);
    return isk;
  }
}

export function createISKDeriver(options: ISKDeriverOptions = {}): ISKDeriver {
  return new ISKDeriver(options);
}

/** Convenience helper for constructing the standard CPace ISK domain string. */
export function withISKSuffix(domainSeparator: BytesLike): Uint8Array {
  return concatBytes(toBytes(domainSeparator, "domainSeparator"), encodeUtf8("_ISK"));
}

export const bytesToHexString = bytesToHex;
