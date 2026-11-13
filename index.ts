/**
 * Frame-header admission for SFrame (RFC 9605) insertable-stream transforms.
 *
 * This module deliberately contains no cryptography. Header syntax and policy
 * checks happen here; authentication/decryption must be supplied by the
 * caller through the `decrypt` (or `authenticate`) callback.
 */

export type SFrameId = number | bigint;
export type SFrameIdInput = SFrameId | string;
export type SFramePartyId = string | number;
export type SFrameBytes = ArrayBufferLike | ArrayBufferView;

export interface SFrameFrameLike {
  data: SFrameBytes;
  [key: string]: unknown;
}

export type SFrameFrame = SFrameBytes | SFrameFrameLike;

export type SFrameErrorCode =
  | "INVALID_FRAME"
  | "TRUNCATED_HEADER"
  | "NON_MINIMAL_ENCODING"
  | "INVALID_HEADER_VALUE"
  | "KID_NOT_REGISTERED"
  | "KID_SENDER_MISMATCH"
  | "KID_ALREADY_REGISTERED"
  | "REPLAY_DETECTED"
  | "AUTHENTICATION_FAILED"
  | "INVALID_DECRYPT_RESULT"
  | "INVALID_ARGUMENT";

export class SFrameError extends Error {
  code: SFrameErrorCode;

  constructor(message: string, code: SFrameErrorCode, cause?: unknown) {
    super(message);
    this.name = "SFrameError";
    this.code = code;
    if (cause !== undefined) {
      (this as unknown as { cause: unknown }).cause = cause;
    }
  }
}

export class SFrameHeaderError extends SFrameError {
  constructor(message: string, code: SFrameErrorCode, cause?: unknown) {
    super(message, code, cause);
    this.name = "SFrameHeaderError";
  }
}

export class SFrameNonMinimalEncodingError extends SFrameHeaderError {
  field: "KID" | "CTR";
  value: bigint;
  encodedLength: number;

  constructor(field: "KID" | "CTR", value: bigint, encodedLength: number) {
    const minimumLength = value <= 7n ? 0 : minimumByteLength(value);
    const expectation =
      value <= 7n
        ? "the value must be carried inline in the config byte"
        : `the value needs ${minimumLength} byte(s)`;
    super(
      `non-minimal SFrame header encoding for ${field}: value ` +
        `0x${value.toString(16)} was encoded in ${encodedLength} byte(s), ` +
        `but ${expectation}`,
      "NON_MINIMAL_ENCODING",
    );
    this.name = "SFrameNonMinimalEncodingError";
    this.field = field;
    this.value = value;
    this.encodedLength = encodedLength;
  }
}

export class SFrameTruncatedHeaderError extends SFrameHeaderError {
  constructor(message: string) {
    super(message, "TRUNCATED_HEADER");
    this.name = "SFrameTruncatedHeaderError";
  }
}

export class SFrameAdmissionError extends SFrameError {
  constructor(message: string, code: SFrameErrorCode, cause?: unknown) {
    super(message, code, cause);
    this.name = "SFrameAdmissionError";
  }
}

export class SFrameKIDRegistrationError extends SFrameAdmissionError {
  constructor(message: string) {
    super(message, "KID_ALREADY_REGISTERED");
    this.name = "SFrameKIDRegistrationError";
  }
}

export class SFrameUnregisteredKIDError extends SFrameAdmissionError {
  trackId: string;
  kid: bigint;

  constructor(trackId: string, kid: bigint) {
    super(
      `SFrame KID 0x${kid.toString(16)} is not registered on track ` +
        `'${trackId}'`,
      "KID_NOT_REGISTERED",
    );
    this.name = "SFrameUnregisteredKIDError";
    this.trackId = trackId;
    this.kid = kid;
  }
}

export class SFrameSenderMismatchError extends SFrameAdmissionError {
  trackId: string;
  kid: bigint;
  expectedSenderId: string;
  actualSenderId: string;

  constructor(
    trackId: string,
    kid: bigint,
    expectedSenderId: string,
    actualSenderId: string,
  ) {
    super(
      `SFrame KID 0x${kid.toString(16)} on track '${trackId}' is ` +
        `registered to sender '${actualSenderId}', not '${expectedSenderId}'`,
      "KID_SENDER_MISMATCH",
    );
    this.name = "SFrameSenderMismatchError";
    this.trackId = trackId;
    this.kid = kid;
    this.expectedSenderId = expectedSenderId;
    this.actualSenderId = actualSenderId;
  }
}

export class SFrameReplayError extends SFrameAdmissionError {
  trackId: string;
  kid: bigint;
  ctr: bigint;
  lastCtr: bigint;

  constructor(trackId: string, kid: bigint, ctr: bigint, lastCtr: bigint) {
    super(
      `replayed or out-of-order SFrame frame: CTR 0x${ctr.toString(16)} ` +
        `for KID 0x${kid.toString(16)} on track '${trackId}' is not ` +
        `greater than last accepted CTR 0x${lastCtr.toString(16)}`,
      "REPLAY_DETECTED",
    );
    this.name = "SFrameReplayError";
    this.trackId = trackId;
    this.kid = kid;
    this.ctr = ctr;
    this.lastCtr = lastCtr;
  }
}

export class SFrameAuthenticationError extends SFrameAdmissionError {
  constructor(message: string, cause?: unknown) {
    super(message, "AUTHENTICATION_FAILED", cause);
    this.name = "SFrameAuthenticationError";
  }
}

const MAX_SAFE_BIGINT = BigInt(Number.MAX_SAFE_INTEGER);
const MAX_ID = (1n << 64n) - 1n;
const GLOBAL_TRACK_ID = "*";

export interface SFrameParseOptions {
  /** Byte offset at which the SFrame header starts. Defaults to zero. */
  byteOffset?: number;
}

/**
 * Parsed RFC 9605 header.
 *
 * `x`, `k`, `y`, and `c` expose the raw config-byte fields. For values that
 * fit in a JS safe integer, `kid` and `ctr` are numbers; values above 2^53-1
 * are returned as bigint. `kidBigInt` and `ctrBigInt` always preserve the
 * complete 64-bit value.
 */
export interface SFrameHeader {
  /** Raw first header byte: X(1) K(3) Y(1) C(3). */
  readonly config: number;
  readonly configByte: number;
  readonly x: boolean;
  readonly X: boolean;
  readonly extendedKid: boolean;
  /** Raw 3-bit K field (KID itself when X=0, length-1 when X=1). */
  readonly k: number;
  readonly K: number;
  readonly y: boolean;
  readonly Y: boolean;
  readonly extendedCtr: boolean;
  /** Raw 3-bit C field (CTR itself when Y=0, length-1 when Y=1). */
  readonly c: number;
  readonly C: number;
  readonly kid: SFrameId;
  readonly kidBigInt: bigint;
  readonly kidNumber: number | null;
  readonly ctr: SFrameId;
  readonly ctrBigInt: bigint;
  readonly ctrNumber: number | null;
  /** Wire bytes used by the KID (0 when carried inline). */
  readonly kidLength: number;
  readonly kidByteLength: number;
  /** Wire bytes used by the CTR (0 when carried inline). */
  readonly ctrLength: number;
  readonly ctrByteLength: number;
  /** Total header length, including the config byte. */
  readonly headerLength: number;
  readonly byteLength: number;
  readonly byteOffset: number;
  /** Absolute offset of the first byte after the header. */
  readonly payloadOffset: number;
  readonly payloadStart: number;
  /** Exact header bytes, suitable as the RFC 9605 AEAD AAD. */
  readonly headerBytes: Uint8Array;
  readonly bytes: Uint8Array;
}

function normalizePartyId(value: unknown, label: string): string {
  if (typeof value === "string") {
    if (value.length === 0) {
      throw new TypeError(`${label} must not be an empty string`);
    }
    return value;
  }
  if (typeof value === "number" && Number.isFinite(value)) {
    return String(value);
  }
  throw new TypeError(`${label} must be a non-empty string or finite number`);
}

function normalizeTrackId(value: unknown, label: string): string {
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number" && Number.isFinite(value)) {
    return String(value);
  }
  throw new TypeError(`${label} must be a string or finite number`);
}

function toBigIntId(value: unknown, label: string): bigint {
  let result: bigint;

  if (typeof value === "bigint") {
    result = value;
  } else if (typeof value === "number") {
    if (!Number.isSafeInteger(value)) {
      throw new TypeError(
        `${label} must be a safe integer when supplied as a number; ` +
          `use bigint for values above 2^53-1`,
      );
    }
    result = BigInt(value);
  } else if (typeof value === "string") {
    const text = value.trim();
    if (/^0x[0-9a-f]+$/i.test(text)) {
      result = BigInt(text);
    } else if (/^[0-9]+$/.test(text)) {
      result = BigInt(text);
    } else {
      throw new TypeError(`${label} must be a decimal or 0x-prefixed integer`);
    }
  } else {
    throw new TypeError(`${label} must be a number, bigint, or integer string`);
  }

  if (result < 0n || result > MAX_ID) {
    throw new RangeError(`${label} must be an unsigned 64-bit integer`);
  }
  return result;
}

function toPublicId(value: bigint): SFrameId {
  return value <= MAX_SAFE_BIGINT ? Number(value) : value;
}

function toSafeNumberOrNull(value: bigint): number | null {
  return value <= MAX_SAFE_BIGINT ? Number(value) : null;
}

function minimumByteLength(value: bigint): number {
  if (value === 0n) {
    return 1;
  }
  let length = 1;
  let remaining = value;
  while (remaining > 0xffn) {
    remaining >>= 8n;
    length += 1;
  }
  return length;
}

function looksLikeIntegerId(value: unknown): boolean {
  if (typeof value === "bigint") {
    return true;
  }
  if (typeof value === "number") {
    return Number.isSafeInteger(value);
  }
  return typeof value === "string" && (/^0x[0-9a-f]+$/i.test(value.trim()) ||
    /^[0-9]+$/.test(value.trim()));
}

function isArrayBufferLike(value: unknown): value is ArrayBufferLike {
  return (
    value instanceof ArrayBuffer ||
    (typeof SharedArrayBuffer !== "undefined" &&
      value instanceof SharedArrayBuffer)
  );
}

interface NormalizedFrame {
  frame: SFrameFrame;
  raw: boolean;
  data: SFrameBytes;
  view: Uint8Array;
}

function normalizeFrame(input: unknown): NormalizedFrame {
  if (ArrayBuffer.isView(input)) {
    return {
      frame: input as SFrameFrame,
      raw: true,
      data: input.buffer,
      view: new Uint8Array(input.buffer, input.byteOffset, input.byteLength),
    };
  }
  if (isArrayBufferLike(input)) {
    return {
      frame: input as SFrameFrame,
      raw: true,
      data: input,
      view: new Uint8Array(input),
    };
  }
  if (input !== null && typeof input === "object" && "data" in input) {
    const frame = input as SFrameFrameLike;
    const data = frame.data;
    if (ArrayBuffer.isView(data)) {
      return {
        frame,
        raw: false,
        data,
        view: new Uint8Array(data.buffer, data.byteOffset, data.byteLength),
      };
    }
    if (isArrayBufferLike(data)) {
      return {
        frame,
        raw: false,
        data,
        view: new Uint8Array(data),
      };
    }
  }
  throw new SFrameError(
    "frame must be an ArrayBuffer, an ArrayBufferView, or an object with " +
      "a binary `data` property",
    "INVALID_FRAME",
  );
}

function parseOptions(
  options: SFrameParseOptions | number | undefined,
): SFrameParseOptions {
  if (options === undefined) {
    return {};
  }
  if (typeof options === "number") {
    return { byteOffset: options };
  }
  if (options === null || typeof options !== "object") {
    throw new TypeError("parse options must be an object or a byte offset");
  }
  return options;
}

function readBigEndian(view: Uint8Array, offset: number, length: number): bigint {
  let value = 0n;
  for (let index = 0; index < length; index += 1) {
    value = (value << 8n) | BigInt(view[offset + index]);
  }
  return value;
}

/**
 * Parse and validate an RFC 9605 SFrame header.
 *
 * Non-minimal encodings are rejected unconditionally: values 0..7 must use
 * the inline K/C field, and larger values must use exactly the number of
 * bytes needed for their magnitude.
 */
export function parseSFrameHeader(
  input: SFrameFrame,
  options?: SFrameParseOptions | number,
): SFrameHeader {
  const normalized = normalizeFrame(input);
  const parsedOptions = parseOptions(options);
  const byteOffset = parsedOptions.byteOffset ?? 0;

  if (
    !Number.isInteger(byteOffset) ||
    byteOffset < 0 ||
    byteOffset > normalized.view.byteLength
  ) {
    throw new SFrameTruncatedHeaderError(
      `SFrame header byte offset ${byteOffset} is outside the frame data`,
    );
  }

  const view = normalized.view;
  if (view.byteLength - byteOffset < 1) {
    throw new SFrameTruncatedHeaderError(
      "truncated SFrame header: missing config byte",
    );
  }

  const config = view[byteOffset];
  const x = (config & 0x80) !== 0;
  const k = (config >>> 4) & 0x07;
  const y = (config & 0x08) !== 0;
  const c = config & 0x07;

  const kidLength = x ? k + 1 : 0;
  const ctrLength = y ? c + 1 : 0;
  const headerLength = 1 + kidLength + ctrLength;

  if (view.byteLength - byteOffset < headerLength) {
    throw new SFrameTruncatedHeaderError(
      `truncated SFrame header: config byte declares ${headerLength} ` +
        `header byte(s), but only ${view.byteLength - byteOffset} are present`,
    );
  }

  let kidBigInt: bigint;
  let ctrBigInt: bigint;
  const kidFieldOffset = byteOffset + 1;
  const ctrFieldOffset = kidFieldOffset + kidLength;

  if (x) {
    kidBigInt = readBigEndian(view, kidFieldOffset, kidLength);
    if (kidBigInt <= 7n || minimumByteLength(kidBigInt) !== kidLength) {
      throw new SFrameNonMinimalEncodingError("KID", kidBigInt, kidLength);
    }
  } else {
    kidBigInt = BigInt(k);
  }

  if (y) {
    ctrBigInt = readBigEndian(view, ctrFieldOffset, ctrLength);
    if (ctrBigInt <= 7n || minimumByteLength(ctrBigInt) !== ctrLength) {
      throw new SFrameNonMinimalEncodingError("CTR", ctrBigInt, ctrLength);
    }
  } else {
    ctrBigInt = BigInt(c);
  }

  const payloadOffset = byteOffset + headerLength;
  const headerBytes = view.slice(byteOffset, payloadOffset);

  return {
    config,
    configByte: config,
    x,
    X: x,
    extendedKid: x,
    k,
    K: k,
    y,
    Y: y,
    extendedCtr: y,
    c,
    C: c,
    kid: toPublicId(kidBigInt),
    kidBigInt,
    kidNumber: toSafeNumberOrNull(kidBigInt),
    ctr: toPublicId(ctrBigInt),
    ctrBigInt,
    ctrNumber: toSafeNumberOrNull(ctrBigInt),
    kidLength,
    kidByteLength: kidLength,
    ctrLength,
    ctrByteLength: ctrLength,
    headerLength,
    byteLength: headerLength,
    byteOffset,
    payloadOffset,
    payloadStart: payloadOffset,
    headerBytes,
    bytes: headerBytes,
  };
}

export interface SFrameHeaderValues {
  kid: SFrameIdInput;
  ctr: SFrameIdInput;
}

function encodeCompactId(
  value: bigint,
  field: "KID" | "CTR",
): { inline: boolean; rawField: number; bytes: Uint8Array } {
  if (value <= 7n) {
    return { inline: true, rawField: Number(value), bytes: new Uint8Array(0) };
  }

  const length = minimumByteLength(value);
  const bytes = new Uint8Array(length);
  let remaining = value;
  for (let index = length - 1; index >= 0; index -= 1) {
    bytes[index] = Number(remaining & 0xffn);
    remaining >>= 8n;
  }
  if (remaining !== 0n) {
    throw new RangeError(`${field} does not fit in 8 bytes`);
  }
  return { inline: false, rawField: length - 1, bytes };
}

/**
 * Encode a canonical RFC 9605 header. Values 0..7 are carried inline and
 * larger values use the minimum possible number of bytes.
 */
export function encodeSFrameHeader(values: SFrameHeaderValues): Uint8Array;
export function encodeSFrameHeader(
  kid: SFrameIdInput,
  ctr: SFrameIdInput,
): Uint8Array;
export function encodeSFrameHeader(
  kidOrValues: SFrameIdInput | SFrameHeaderValues,
  maybeCtr?: SFrameIdInput,
): Uint8Array {
  let kidInput: SFrameIdInput;
  let ctrInput: SFrameIdInput;

  if (
    typeof kidOrValues === "object" &&
    kidOrValues !== null &&
    "kid" in kidOrValues &&
    "ctr" in kidOrValues
  ) {
    kidInput = kidOrValues.kid;
    ctrInput = kidOrValues.ctr;
  } else {
    if (maybeCtr === undefined) {
      throw new TypeError("both KID and CTR are required to encode a header");
    }
    kidInput = kidOrValues as SFrameIdInput;
    ctrInput = maybeCtr;
  }

  const kid = toBigIntId(kidInput, "KID");
  const ctr = toBigIntId(ctrInput, "CTR");
  const encodedKid = encodeCompactId(kid, "KID");
  const encodedCtr = encodeCompactId(ctr, "CTR");

  let config = 0;
  if (!encodedKid.inline) {
    config |= 0x80;
  }
  config |= encodedKid.rawField << 4;
  if (!encodedCtr.inline) {
    config |= 0x08;
  }
  config |= encodedCtr.rawField;

  const result = new Uint8Array(
    1 + encodedKid.bytes.byteLength + encodedCtr.bytes.byteLength,
  );
  result[0] = config;
  result.set(encodedKid.bytes, 1);
  result.set(encodedCtr.bytes, 1 + encodedKid.bytes.byteLength);
  return result;
}

export interface SFrameKIDRegistration {
  trackId: SFramePartyId;
  kid: SFrameIdInput;
  senderId: SFramePartyId;
}

export interface SFrameKIDRegistryOptions {
  /** Track used by the two-argument `register(kid, senderId)` form. */
  trackId?: SFramePartyId;
}

interface RegisteredKID {
  kid: bigint;
  senderId: string;
}

/**
 * Track-scoped KID ownership registry.
 *
 * A KID may be registered only to one sender on a given track. Registering
 * the same (track, KID, sender) pair twice is idempotent; assigning it to a
 * different sender is an error.
 */
export class SFrameKIDRegistry {
  readonly defaultTrackId: string | undefined;
  #byTrack = new Map<string, Map<string, RegisteredKID>>();

  constructor(options: SFrameKIDRegistryOptions | SFramePartyId = {}) {
    if (
      typeof options === "string" ||
      (typeof options === "number" && Number.isFinite(options))
    ) {
      this.defaultTrackId = normalizeTrackId(options, "trackId");
    } else if (options !== null && typeof options === "object") {
      this.defaultTrackId =
        options.trackId === undefined
          ? undefined
          : normalizeTrackId(options.trackId, "trackId");
    } else {
      throw new TypeError("registry options must be an object or a track ID");
    }
  }

  get size(): number {
    let count = 0;
    for (const registrations of this.#byTrack.values()) {
      count += registrations.size;
    }
    return count;
  }

  register(registration: SFrameKIDRegistration): this;
  register(
    trackId: SFramePartyId,
    kid: SFrameIdInput,
    senderId: SFramePartyId,
  ): this;
  register(kid: SFrameIdInput, senderId: SFramePartyId): this;
  register(
    trackOrRegistrationOrKid:
      | SFramePartyId
      | SFrameKIDRegistration
      | SFrameIdInput,
    kidOrSender?: SFrameIdInput | SFramePartyId,
    senderId?: SFramePartyId,
  ): this {
    let trackId: string;
    let kid: bigint;
    let sender: string;

    if (
      trackOrRegistrationOrKid !== null &&
      typeof trackOrRegistrationOrKid === "object" &&
      "trackId" in trackOrRegistrationOrKid &&
      "kid" in trackOrRegistrationOrKid &&
      "senderId" in trackOrRegistrationOrKid
    ) {
      const registration = trackOrRegistrationOrKid;
      trackId = normalizeTrackId(registration.trackId, "trackId");
      kid = toBigIntId(registration.kid, "KID");
      sender = normalizePartyId(registration.senderId, "senderId");
    } else if (senderId === undefined) {
      if (kidOrSender === undefined) {
        throw new TypeError("must provide sender ID and KID");
      }
      trackId = this.defaultTrackId ?? GLOBAL_TRACK_ID;
      kid = toBigIntId(trackOrRegistrationOrKid, "KID");
      sender = normalizePartyId(kidOrSender, "senderId");
    } else {
      // Preferred positional form: register(trackId, kid, senderId).
      // Also accept register(kid, senderId, trackId) when the first argument
      // is unambiguously an integer ID.
      const first = trackOrRegistrationOrKid;
      const second = kidOrSender;
      if (looksLikeIntegerId(first) && typeof second === "string") {
        kid = toBigIntId(first, "KID");
        sender = normalizePartyId(second, "senderId");
        trackId = normalizeTrackId(senderId, "trackId");
      } else {
        trackId = normalizeTrackId(first, "trackId");
        kid = toBigIntId(second, "KID");
        sender = normalizePartyId(senderId, "senderId");
      }
    }

    let registrations = this.#byTrack.get(trackId);
    if (registrations === undefined) {
      registrations = new Map<string, RegisteredKID>();
      this.#byTrack.set(trackId, registrations);
    }

    const key = kid.toString(10);
    const existing = registrations.get(key);
    if (existing !== undefined) {
      if (existing.senderId === sender) {
        return this;
      }
      throw new SFrameKIDRegistrationError(
        `SFrame KID 0x${kid.toString(16)} on track '${trackId}' is already ` +
          `registered to sender '${existing.senderId}'`,
      );
    }

    registrations.set(key, { kid, senderId: sender });
    return this;
  }

  /** Alias for `register`. */
  registerKID(registration: SFrameKIDRegistration): this;
  registerKID(
    trackId: SFramePartyId,
    kid: SFrameIdInput,
    senderId: SFramePartyId,
  ): this;
  registerKID(kid: SFrameIdInput, senderId: SFramePartyId): this;
  registerKID(
    trackOrRegistrationOrKid:
      | SFramePartyId
      | SFrameKIDRegistration
      | SFrameIdInput,
    kidOrSender?: SFrameIdInput | SFramePartyId,
    senderId?: SFramePartyId,
  ): this {
    const register = this.register as unknown as (
      first: unknown,
      second?: unknown,
      third?: unknown,
    ) => this;
    return register.call(
      this,
      trackOrRegistrationOrKid,
      kidOrSender,
      senderId,
    );
  }

  senderFor(
    trackId: SFramePartyId,
    kidInput: SFrameIdInput,
  ): string | undefined {
    const track = normalizeTrackId(trackId, "trackId");
    const kid = toBigIntId(kidInput, "KID");
    const key = kid.toString(10);
    return (
      this.#byTrack.get(track)?.get(key)?.senderId ??
      this.#byTrack.get(GLOBAL_TRACK_ID)?.get(key)?.senderId
    );
  }

  /** Alias for `senderFor`. */
  getSender(
    trackId: SFramePartyId,
    kidInput: SFrameIdInput,
  ): string | undefined {
    return this.senderFor(trackId, kidInput);
  }

  has(trackId: SFramePartyId, kidInput: SFrameIdInput): boolean {
    return this.senderFor(trackId, kidInput) !== undefined;
  }

  assertOwnedBy(
    trackId: SFramePartyId,
    kidInput: SFrameIdInput,
    senderId: SFramePartyId,
  ): void {
    const track = normalizeTrackId(trackId, "trackId");
    const kid = toBigIntId(kidInput, "KID");
    const expectedSender = normalizePartyId(senderId, "senderId");
    const owner = this.senderFor(track, kid);
    if (owner === undefined) {
      throw new SFrameUnregisteredKIDError(track, kid);
    }
    if (owner !== expectedSender) {
      throw new SFrameSenderMismatchError(track, kid, expectedSender, owner);
    }
  }

  /** Alias for `assertOwnedBy`. */
  assertKIDOwner(
    trackId: SFramePartyId,
    kidInput: SFrameIdInput,
    senderId: SFramePartyId,
  ): void {
    this.assertOwnedBy(trackId, kidInput, senderId);
  }

  unregister(trackId: SFramePartyId, kidInput: SFrameIdInput): boolean {
    const track = normalizeTrackId(trackId, "trackId");
    const kid = toBigIntId(kidInput, "KID");
    const registrations = this.#byTrack.get(track);
    if (registrations === undefined) {
      return false;
    }
    const removed = registrations.delete(kid.toString(10));
    if (registrations.size === 0) {
      this.#byTrack.delete(track);
    }
    return removed;
  }

  clearTrack(trackId: SFramePartyId): void {
    this.#byTrack.delete(normalizeTrackId(trackId, "trackId"));
  }

  clear(): void {
    this.#byTrack.clear();
  }

  entries(): Array<{ trackId: string; kid: bigint; senderId: string }> {
    const result: Array<{ trackId: string; kid: bigint; senderId: string }> =
      [];
    for (const [trackId, registrations] of this.#byTrack) {
      for (const registration of registrations.values()) {
        result.push({
          trackId,
          kid: registration.kid,
          senderId: registration.senderId,
        });
      }
    }
    return result;
  }
}

/**
 * Replay state for accepted SFrame frames.
 *
 * `check` never mutates state. Callers must call `commit` only after the
 * injected authentication/decryption succeeds, so a forged frame with a
 * future CTR cannot poison the replay window.
 */
export class SFrameReplayGuard {
  #last = new Map<string, Map<string, bigint>>();

  lastCtr(
    trackId: SFramePartyId,
    kidInput: SFrameIdInput,
  ): bigint | undefined {
    const track = normalizeTrackId(trackId, "trackId");
    const kid = toBigIntId(kidInput, "KID");
    return this.#last.get(track)?.get(kid.toString(10));
  }

  lastValue(
    trackId: SFramePartyId,
    kidInput: SFrameIdInput,
  ): SFrameId | undefined {
    const value = this.lastCtr(trackId, kidInput);
    return value === undefined ? undefined : toPublicId(value);
  }

  check(
    trackId: SFramePartyId,
    kidInput: SFrameIdInput,
    ctrInput: SFrameIdInput,
  ): void {
    const track = normalizeTrackId(trackId, "trackId");
    const kid = toBigIntId(kidInput, "KID");
    const ctr = toBigIntId(ctrInput, "CTR");
    const last = this.#last.get(track)?.get(kid.toString(10));
    if (last !== undefined && ctr <= last) {
      throw new SFrameReplayError(track, kid, ctr, last);
    }
  }

  commit(
    trackId: SFramePartyId,
    kidInput: SFrameIdInput,
    ctrInput: SFrameIdInput,
  ): void {
    const track = normalizeTrackId(trackId, "trackId");
    const kid = toBigIntId(kidInput, "KID");
    const ctr = toBigIntId(ctrInput, "CTR");
    let byKid = this.#last.get(track);
    if (byKid === undefined) {
      byKid = new Map<string, bigint>();
      this.#last.set(track, byKid);
    }
    const key = kid.toString(10);
    const last = byKid.get(key);
    if (last !== undefined && ctr <= last) {
      throw new SFrameReplayError(track, kid, ctr, last);
    }
    byKid.set(key, ctr);
  }

  reset(trackId?: SFramePartyId, kidInput?: SFrameIdInput): void {
    if (trackId === undefined) {
      this.#last.clear();
      return;
    }
    const track = normalizeTrackId(trackId, "trackId");
    if (kidInput === undefined) {
      this.#last.delete(track);
      return;
    }
    const kid = toBigIntId(kidInput, "KID");
    const byKid = this.#last.get(track);
    if (byKid === undefined) {
      return;
    }
    byKid.delete(kid.toString(10));
    if (byKid.size === 0) {
      this.#last.delete(track);
    }
  }

  clear(): void {
    this.#last.clear();
  }
}

export interface SFrameAdmissionContext extends SFrameHeader {
  readonly header: SFrameHeader;
  readonly frame: SFrameFrame;
  readonly data: SFrameBytes;
  /** Frame bytes following the SFrame header. */
  readonly payload: Uint8Array;
  readonly trackId: string;
  readonly senderId: string;
}

export interface SFrameDecryptOutput {
  accepted?: boolean;
  data?: SFrameBytes;
  plaintext?: SFrameBytes;
  frame?: SFrameFrame;
}

export type SFrameDecryptResult =
  | void
  | null
  | boolean
  | SFrameBytes
  | SFrameDecryptOutput;

export type SFrameAuthenticatedDecryptor = (
  context: SFrameAdmissionContext,
  payload: Uint8Array,
  frame: SFrameFrame,
) => SFrameDecryptResult | Promise<SFrameDecryptResult>;

export interface SFrameFrameAdmissionOptions {
  registry?: SFrameKIDRegistry;
  /** Alias for `registry`. */
  kidRegistry?: SFrameKIDRegistry;
  trackId?: SFramePartyId;
  /** Alias for `trackId`. */
  track?: SFramePartyId;
  senderId?: SFramePartyId;
  /** Alias for `senderId`. */
  sender?: SFramePartyId;

  /**
   * Injected authenticated-decryption function. Return false (or throw) to
   * reject the frame. Return a byte source to replace `frame.data`, or a
   * `{ frame }` object to replace the frame itself. Returning undefined,
   * null, or true accepts the frame unchanged.
   */
  decrypt?: SFrameAuthenticatedDecryptor;
  /** Alias for `decrypt`. */
  authenticate?: SFrameAuthenticatedDecryptor;
  /** Alias for `decrypt`. */
  authenticateAndDecrypt?: SFrameAuthenticatedDecryptor;
  /** Alias for `decrypt`. */
  decryptor?: SFrameAuthenticatedDecryptor;

  replayGuard?: SFrameReplayGuard;
  /** Offset of the SFrame header within frame.data. Defaults to zero. */
  headerOffset?: number;
  /** How `createTransform()` handles a rejected frame. Defaults to "drop". */
  rejectBehavior?: "drop" | "throw" | "error";
  onReject?: (error: SFrameError, frame: SFrameFrame) => void;
  /** Explicit opt-out used only for header-only deployments. */
  allowUnauthenticated?: boolean;
}

function asSFrameError(error: unknown): SFrameError {
  if (error instanceof SFrameError) {
    return error;
  }
  if (error instanceof Error) {
    return new SFrameError(error.message, "INVALID_ARGUMENT", error);
  }
  return new SFrameError(String(error), "INVALID_ARGUMENT", error);
}

function isByteSource(value: unknown): value is SFrameBytes {
  return ArrayBuffer.isView(value) || isArrayBufferLike(value);
}

function bytesFrom(value: SFrameBytes): Uint8Array {
  if (ArrayBuffer.isView(value)) {
    return new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
  }
  return new Uint8Array(value);
}

function replaceFrameData(frame: SFrameFrame, bytes: Uint8Array): SFrameFrame {
  if (ArrayBuffer.isView(frame) || isArrayBufferLike(frame)) {
    return bytes;
  }

  try {
    (frame as { data: SFrameBytes }).data = bytes;
    if ((frame as { data: SFrameBytes }).data === bytes) {
      return frame;
    }
  } catch {
    // Fall through to a shallow clone for read-only frame objects.
  }

  const clone = Object.create(Object.getPrototypeOf(frame));
  Object.assign(clone, frame, { data: bytes });
  return clone;
}

function applyDecryptResult(
  original: SFrameFrame,
  result: SFrameDecryptResult,
): SFrameFrame {
  if (result === undefined || result === null || result === true) {
    return original;
  }
  if (result === false) {
    throw new SFrameAuthenticationError(
      "the injected authenticated decryptor rejected the frame",
    );
  }
  if (isByteSource(result)) {
    return replaceFrameData(original, bytesFrom(result));
  }
  if (result instanceof Error) {
    throw new SFrameAuthenticationError(
      "the injected authenticated decryptor returned an error",
      result,
    );
  }
  if (typeof result === "object") {
    if (result.accepted === false) {
      throw new SFrameAuthenticationError(
        "the injected authenticated decryptor rejected the frame",
      );
    }
    if ("frame" in result && result.frame !== undefined) {
      return result.frame;
    }
    const replacement = result.data ?? result.plaintext;
    if (replacement !== undefined) {
      if (!isByteSource(replacement)) {
        throw new SFrameAdmissionError(
          "the injected authenticated decryptor returned non-binary frame data",
          "INVALID_DECRYPT_RESULT",
        );
      }
      return replaceFrameData(original, bytesFrom(replacement));
    }
    return original;
  }
  throw new SFrameAdmissionError(
    "the injected authenticated decryptor returned an invalid result",
    "INVALID_DECRYPT_RESULT",
  );
}

/**
 * Stateful admission gate for one track and one expected sender.
 *
 * The asynchronous `admit` path serializes frames sharing a KID, checks the
 * replay window before authentication and commits the CTR only after the
 * injected decryptor succeeds.
 */
export class SFrameFrameAdmission {
  readonly registry: SFrameKIDRegistry;
  readonly trackId: string;
  readonly senderId: string;
  readonly replayGuard: SFrameReplayGuard;
  readonly headerOffset: number;
  readonly rejectBehavior: "drop" | "throw";
  #decrypt: SFrameAuthenticatedDecryptor | undefined;
  #onReject: ((error: SFrameError, frame: SFrameFrame) => void) | undefined;
  #locks = new Map<string, Promise<void>>();

  constructor(options: SFrameFrameAdmissionOptions) {
    if (options === null || typeof options !== "object") {
      throw new TypeError("admission options must be an object");
    }
    const registry = options.registry ?? options.kidRegistry;
    if (!(registry instanceof SFrameKIDRegistry)) {
      throw new TypeError(
        "registry (or kidRegistry) must be an SFrameKIDRegistry",
      );
    }

    const trackId =
      options.trackId ?? options.track ?? registry.defaultTrackId;
    if (trackId === undefined) {
      throw new TypeError(
        "trackId is required unless the registry has a default trackId",
      );
    }

    this.registry = registry;
    this.trackId = normalizeTrackId(trackId, "trackId");
    const senderId = options.senderId ?? options.sender;
    if (senderId === undefined) {
      throw new TypeError("senderId (or sender) is required");
    }
    this.senderId = normalizePartyId(senderId, "senderId");
    this.replayGuard = options.replayGuard ?? new SFrameReplayGuard();
    this.headerOffset = options.headerOffset ?? 0;
    if (!Number.isInteger(this.headerOffset) || this.headerOffset < 0) {
      throw new TypeError("headerOffset must be a non-negative integer");
    }

    const behavior = options.rejectBehavior ?? "drop";
    if (behavior !== "drop" && behavior !== "throw" && behavior !== "error") {
      throw new TypeError('rejectBehavior must be "drop", "throw", or "error"');
    }
    this.rejectBehavior = behavior === "drop" ? "drop" : "throw";

    this.#decrypt =
      options.decrypt ??
      options.authenticate ??
      options.authenticateAndDecrypt ??
      options.decryptor;
    if (this.#decrypt !== undefined && typeof this.#decrypt !== "function") {
      throw new TypeError("decrypt must be a function");
    }
    if (this.#decrypt === undefined && options.allowUnauthenticated !== true) {
      throw new TypeError(
        "an injected decrypt/authenticate callback is required " +
          "(or set allowUnauthenticated explicitly)",
      );
    }

    if (
      options.onReject !== undefined &&
      typeof options.onReject !== "function"
    ) {
      throw new TypeError("onReject must be a function");
    }
    this.#onReject = options.onReject;
  }

  /**
   * Perform syntax, KID-ownership, and replay checks without authentication
   * and without advancing replay state. Useful for policy tests and for
   * callers that authenticate elsewhere.
   */
  check(frame: SFrameFrame): SFrameHeader {
    const normalized = normalizeFrame(frame);
    const header = parseSFrameHeader(normalized.frame, {
      byteOffset: this.headerOffset,
    });
    this.registry.assertOwnedBy(
      this.trackId,
      header.kidBigInt,
      this.senderId,
    );
    this.replayGuard.check(this.trackId, header.kidBigInt, header.ctrBigInt);
    return header;
  }

  lastAcceptedCtr(kidInput: SFrameIdInput): SFrameId | undefined {
    return this.replayGuard.lastValue(this.trackId, kidInput);
  }

  #lockKey(kid: bigint): string {
    return JSON.stringify([this.trackId, kid.toString(10)]);
  }

  async #withKIDLock<T>(kid: bigint, operation: () => Promise<T>): Promise<T> {
    const key = this.#lockKey(kid);
    const previous = this.#locks.get(key) ?? Promise.resolve();
    let release = (): void => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const queued = previous.then(() => gate);
    this.#locks.set(key, queued);

    await previous;
    try {
      return await operation();
    } finally {
      release();
      if (this.#locks.get(key) === queued) {
        this.#locks.delete(key);
      }
    }
  }

  /**
   * Admit one frame or throw an `SFrameError` subclass.
   *
   * Replay state is committed only after the injected decryptor succeeds.
   */
  async admit(frame: SFrameFrame): Promise<SFrameFrame> {
    const normalized = normalizeFrame(frame);
    let header: SFrameHeader;
    try {
      header = parseSFrameHeader(normalized.frame, {
        byteOffset: this.headerOffset,
      });
      this.registry.assertOwnedBy(
        this.trackId,
        header.kidBigInt,
        this.senderId,
      );
    } catch (error) {
      throw asSFrameError(error);
    }

    return this.#withKIDLock(header.kidBigInt, async () => {
      this.replayGuard.check(this.trackId, header.kidBigInt, header.ctrBigInt);

      const payload = normalized.view.subarray(header.payloadOffset);

      if (this.#decrypt !== undefined) {
        const context: SFrameAdmissionContext = {
          ...header,
          header,
          frame: normalized.frame,
          data: normalized.data,
          payload,
          trackId: this.trackId,
          senderId: this.senderId,
        };

        let result: SFrameDecryptResult;
        try {
          result = await this.#decrypt(context, payload, normalized.frame);
        } catch (error) {
          if (error instanceof SFrameError) {
            throw error;
          }
          throw new SFrameAuthenticationError(
            "the injected authenticated decryptor failed",
            error,
          );
        }

        const output = applyDecryptResult(normalized.frame, result);
        this.replayGuard.commit(
          this.trackId,
          header.kidBigInt,
          header.ctrBigInt,
        );
        return output;
      }

      this.replayGuard.commit(
        this.trackId,
        header.kidBigInt,
        header.ctrBigInt,
      );
      return normalized.frame;
    });
  }

  /** Alias for `admit`. */
  admitFrame(frame: SFrameFrame): Promise<SFrameFrame> {
    return this.admit(frame);
  }

  /** Create a WebRTC insertable-stream-compatible TransformStream. */
  createTransform(): TransformStream<SFrameFrame, SFrameFrame> {
    const admission = this;
    return new TransformStream<SFrameFrame, SFrameFrame>({
      async transform(frame, controller) {
        try {
          const output = await admission.admit(frame);
          controller.enqueue(output);
        } catch (error) {
          const admissionError = asSFrameError(error);
          if (admission.#onReject !== undefined) {
            try {
              admission.#onReject(admissionError, frame);
            } catch {
              // A logging callback must not change admission behavior.
            }
          }
          if (admission.rejectBehavior === "throw") {
            controller.error(admissionError);
          }
          // Otherwise silently drop the frame: an attacker cannot use a
          // rejected frame to tear down the whole media pipeline.
        }
      },
    });
  }
}

/** Create a TransformStream directly from admission options. */
export function createSFrameAdmissionTransform(
  options: SFrameFrameAdmissionOptions,
): TransformStream<SFrameFrame, SFrameFrame>;
export function createSFrameAdmissionTransform(
  admission: SFrameFrameAdmission,
): TransformStream<SFrameFrame, SFrameFrame>;
export function createSFrameAdmissionTransform(
  optionsOrAdmission: SFrameFrameAdmissionOptions | SFrameFrameAdmission,
): TransformStream<SFrameFrame, SFrameFrame> {
  const admission =
    optionsOrAdmission instanceof SFrameFrameAdmission
      ? optionsOrAdmission
      : new SFrameFrameAdmission(optionsOrAdmission);
  return admission.createTransform();
}

/**
 * Synchronous convenience check: parse, verify the KID is registered to the
 * expected sender, and verify it is newer than the last accepted CTR.
 * This does not authenticate or mutate replay state.
 */
export function checkSFrameHeader(
  frame: SFrameFrame,
  options: {
    registry?: SFrameKIDRegistry;
    kidRegistry?: SFrameKIDRegistry;
    trackId?: SFramePartyId;
    senderId?: SFramePartyId;
    sender?: SFramePartyId;
    replayGuard?: SFrameReplayGuard;
    headerOffset?: number;
  },
): SFrameHeader {
  const admission = new SFrameFrameAdmission({
    registry: options.registry,
    kidRegistry: options.kidRegistry,
    trackId: options.trackId,
    senderId: options.senderId,
    sender: options.sender,
    replayGuard: options.replayGuard,
    headerOffset: options.headerOffset,
    allowUnauthenticated: true,
  });
  return admission.check(frame);
}

// Conventional aliases for consumers that use different SFrame terminology.
export { parseSFrameHeader as decodeSFrameHeader };
export { parseSFrameHeader as parseFrameHeader };
export { encodeSFrameHeader as encodeFrameHeader };
export { SFrameKIDRegistry as KIDRegistry };
export { SFrameReplayGuard as ReplayGuard };
export { SFrameReplayGuard as ReplayWindow };
export { SFrameFrameAdmission as SFrameAdmission };
export { SFrameFrameAdmission as FrameAdmission };
export { SFrameFrameAdmission as SFrameHeaderAdmission };
export { SFrameFrameAdmission as FrameHeaderAdmission };
export { SFrameNonMinimalEncodingError as NonMinimalEncodingError };
export { createSFrameAdmissionTransform as createAdmissionTransform };
export { createSFrameAdmissionTransform as createFrameAdmissionTransform };
export { createSFrameAdmissionTransform as createSFrameTransform };

const defaultExport = {
  parseSFrameHeader,
  encodeSFrameHeader,
  SFrameKIDRegistry,
  SFrameReplayGuard,
  SFrameFrameAdmission,
  createSFrameAdmissionTransform,
  checkSFrameHeader,
};

export default defaultExport;
