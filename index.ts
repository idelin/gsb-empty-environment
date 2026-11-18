/**
 * RFC 6455 subprotocol negotiation with token extraction and message-type
 * authorization.
 *
 * Security invariant: values matching `bearer.<token>` are never treated as
 * subprotocol candidates, returned in response headers, or written to the
 * handshake log record. The extracted token is only exposed via
 * `HandshakeResult.token`.
 */

export const SEC_WEBSOCKET_PROTOCOL = "Sec-WebSocket-Protocol";
export const UNAUTHORIZED_MESSAGE_CLOSE_CODE = 1008;
export const UNAUTHORIZED_MESSAGE_REASON = "Message type is not permitted";

const TOKEN_RE = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;
const BEARER_PREFIX = "bearer.";
const BEARER_NAMESPACE_RE = /^bearer\./i;

export type HandshakeHeader = string | readonly string[] | null | undefined;

export interface SubprotocolRule {
  readonly name: string;
  readonly messageTypes?: readonly string[];
}

export type SubprotocolInput =
  | readonly SubprotocolRule[]
  | Readonly<Record<string, readonly string[]>>
  | ReadonlyMap<string, readonly string[]>;

export interface NormalizedSubprotocolRule {
  readonly name: string;
  readonly messageTypes: readonly string[];
}

/**
 * A validated, immutable server-side protocol priority list.
 *
 * Build one at startup with `createSubprotocolPolicy`; reusing it also avoids
 * re-validating configuration on every connection.
 */
export class SubprotocolPolicy {
  readonly entries: readonly NormalizedSubprotocolRule[];

  constructor(input: SubprotocolInput) {
    this.entries = normalizeServerProtocols(input);
    Object.freeze(this);
  }
}

export function createSubprotocolPolicy(
  input: SubprotocolPolicy | SubprotocolInput,
): SubprotocolPolicy {
  return input instanceof SubprotocolPolicy
    ? input
    : new SubprotocolPolicy(input);
}

export function isSubprotocolToken(value: string): boolean {
  return typeof value === "string" && TOKEN_RE.test(value);
}

export type NegotiationErrorCode =
  | "duplicate_client_protocol"
  | "invalid_client_protocol"
  | "multiple_bearer_tokens";

export interface NegotiationErrorInfo {
  readonly code: NegotiationErrorCode;
  readonly message: string;
  readonly statusCode: 400;
}

export interface ParsedSubprotocolHeaderSuccess {
  readonly ok: true;
  readonly protocols: readonly string[];
  readonly token: string | null;
  readonly hasBearerToken: boolean;
  readonly offeredProtocolCount: number;
}

export interface ParsedSubprotocolHeaderFailure {
  readonly ok: false;
  readonly error: NegotiationErrorInfo;
  readonly hasBearerToken: boolean;
  readonly offeredProtocolCount: number;
}

export type ParsedSubprotocolHeader =
  | ParsedSubprotocolHeaderSuccess
  | ParsedSubprotocolHeaderFailure;

export interface HandshakeNegotiationOptions {
  readonly header?: HandshakeHeader;
  readonly protocols: SubprotocolPolicy | SubprotocolInput;
  readonly logger?: HandshakeLogger;
}

interface HandshakeResultBase {
  readonly accepted: boolean;
  readonly statusCode: 101 | 400;
  readonly subprotocol: string | null;
  readonly hasSubprotocol: boolean;
  readonly token: string | null;
  readonly hasBearerToken: boolean;
  readonly offeredProtocolCount: number;
  readonly allowedMessageTypes: readonly string[];
  readonly responseHeaders: Readonly<Record<string, string>>;
}

export interface HandshakeSuccess extends HandshakeResultBase {
  readonly accepted: true;
  readonly statusCode: 101;
  readonly error: null;
}

export interface HandshakeFailure extends HandshakeResultBase {
  readonly accepted: false;
  readonly statusCode: 400;
  readonly subprotocol: null;
  readonly hasSubprotocol: false;
  readonly token: null;
  readonly allowedMessageTypes: readonly never[];
  readonly error: NegotiationErrorInfo;
}

export type HandshakeResult = HandshakeSuccess | HandshakeFailure;

export interface SafeHandshakeLogRecord {
  readonly event: "websocket_handshake";
  readonly accepted: boolean;
  readonly statusCode: 101 | 400;
  readonly subprotocol: string | null;
  readonly hasBearerToken: boolean;
  readonly offeredProtocolCount: number;
  readonly allowedMessageTypes: readonly string[];
  readonly reason: NegotiationErrorCode | null;
}

export type HandshakeLogger = (record: SafeHandshakeLogRecord) => void;

export interface ClosableWebSocket {
  close(code?: number, reason?: string): void;
}

const EMPTY_HEADERS: Readonly<Record<string, string>> = Object.freeze({});
const EMPTY_MESSAGE_TYPES: readonly never[] = Object.freeze([]);

export function parseSubprotocolHeader(
  header: HandshakeHeader,
): ParsedSubprotocolHeader {
  const headerText = joinHeaderValue(header);

  if (headerText === null) {
    const parsed: ParsedSubprotocolHeaderSuccess = {
      ok: true,
      protocols: Object.freeze([]),
      token: null,
      hasBearerToken: false,
      offeredProtocolCount: 0,
    };
    return Object.freeze(parsed);
  }

  const values = headerText.split(",").map((value) => value.trim());
  const hasBearerToken = values.some(
    (value) =>
      value.length > BEARER_PREFIX.length &&
      value.startsWith(BEARER_PREFIX),
  );

  if (values.length === 0 || values.some((value) => value.length === 0)) {
    return parsedHeaderFailure(
      "invalid_client_protocol",
      "The client subprotocol list contains an empty value.",
      hasBearerToken,
      values.length,
    );
  }

  const seen = new Set<string>();
  const protocols: string[] = [];
  let token: string | null = null;

  for (const value of values) {
    if (!isSubprotocolToken(value)) {
      return parsedHeaderFailure(
        "invalid_client_protocol",
        "The client subprotocol list contains a value that is not an RFC 6455 token.",
        hasBearerToken,
        values.length,
      );
    }

    if (seen.has(value)) {
      return parsedHeaderFailure(
        "duplicate_client_protocol",
        "The client subprotocol list contains a duplicate name.",
        hasBearerToken,
        values.length,
      );
    }
    seen.add(value);

    if (value.startsWith(BEARER_PREFIX)) {
      if (value.length === BEARER_PREFIX.length) {
        return parsedHeaderFailure(
          "invalid_client_protocol",
          "The bearer subprotocol value has an empty token.",
          true,
          values.length,
        );
      }

      if (token !== null) {
        return parsedHeaderFailure(
          "multiple_bearer_tokens",
          "The client subprotocol list contains more than one bearer token.",
          true,
          values.length,
        );
      }

      token = value.slice(BEARER_PREFIX.length);
      continue;
    }

    protocols.push(value);
  }

  const parsed: ParsedSubprotocolHeaderSuccess = {
    ok: true,
    protocols: Object.freeze(protocols),
    token,
    hasBearerToken: token !== null,
    offeredProtocolCount: values.length,
  };
  return Object.freeze(parsed);
}

/**
 * Negotiate one subprotocol from the client's `Sec-WebSocket-Protocol` value.
 *
 * Server order is authoritative. A missing intersection is a successful
 * handshake with `subprotocol: null` and no response header. Malformed or
 * duplicate client values reject the handshake with HTTP status 400.
 */
export function negotiateHandshake(
  options: HandshakeNegotiationOptions,
): HandshakeResult {
  if (options === null || typeof options !== "object") {
    throw new TypeError("Handshake negotiation options are required.");
  }
  if (options.logger !== undefined && typeof options.logger !== "function") {
    throw new TypeError("The handshake logger must be a function.");
  }

  const policy = createSubprotocolPolicy(options.protocols);
  const parsed = parseSubprotocolHeader(options.header);
  const result = parsed.ok
    ? negotiateParsedHeader(parsed, policy)
    : handshakeFailure(
        parsed.error,
        parsed.offeredProtocolCount,
        parsed.hasBearerToken,
      );

  emitSafeHandshakeLog(options.logger, result);
  return result;
}

/**
 * Positional convenience wrapper around `negotiateHandshake`.
 */
export function negotiateSubprotocol(
  header: HandshakeHeader,
  protocols: SubprotocolPolicy | SubprotocolInput,
  logger?: HandshakeLogger,
): HandshakeResult {
  return negotiateHandshake({ header, protocols, logger });
}

export const negotiateWebSocketHandshake = negotiateHandshake;

export function isMessageTypeAllowed(
  connection: Pick<HandshakeResultBase, "allowedMessageTypes">,
  messageType: string,
): boolean {
  return (
    typeof messageType === "string" &&
    connection.allowedMessageTypes.includes(messageType)
  );
}

export function messageTypeCloseCode(
  connection: Pick<HandshakeResultBase, "allowedMessageTypes">,
  messageType: string,
): 1008 | null {
  return isMessageTypeAllowed(connection, messageType)
    ? null
    : UNAUTHORIZED_MESSAGE_CLOSE_CODE;
}

export const closeCodeForMessageType = messageTypeCloseCode;

export function shouldCloseForMessageType(
  connection: Pick<HandshakeResultBase, "allowedMessageTypes">,
  messageType: string,
): boolean {
  return !isMessageTypeAllowed(connection, messageType);
}

/**
 * Enforce the selected subprotocol's message-type allowlist.
 *
 * Returns `true` when the message is allowed. Otherwise closes the socket
 * immediately with code 1008 and returns `false`.
 */
export function enforceMessageType(
  connection: Pick<HandshakeResultBase, "allowedMessageTypes">,
  messageType: string,
  socket: ClosableWebSocket,
): boolean {
  if (isMessageTypeAllowed(connection, messageType)) {
    return true;
  }

  socket.close(UNAUTHORIZED_MESSAGE_CLOSE_CODE, UNAUTHORIZED_MESSAGE_REASON);
  return false;
}

/**
 * Convert a result to a safe audit record. This deliberately excludes the
 * bearer token and all unselected client protocol names.
 */
export function handshakeLogRecord(
  result: HandshakeResult,
): SafeHandshakeLogRecord {
  const record: SafeHandshakeLogRecord = {
    event: "websocket_handshake",
    accepted: result.accepted,
    statusCode: result.statusCode,
    subprotocol: result.subprotocol,
    hasBearerToken: result.hasBearerToken,
    offeredProtocolCount: result.offeredProtocolCount,
    allowedMessageTypes: result.allowedMessageTypes,
    reason: result.error?.code ?? null,
  };
  return Object.freeze(record);
}

function negotiateParsedHeader(
  parsed: ParsedSubprotocolHeaderSuccess,
  policy: SubprotocolPolicy,
): HandshakeResult {
  const requested = new Set(parsed.protocols);
  const selected = policy.entries.find((entry) => requested.has(entry.name));

  if (selected === undefined) {
    const result: HandshakeSuccess = {
      accepted: true,
      statusCode: 101,
      subprotocol: null,
      hasSubprotocol: false,
      token: parsed.token,
      hasBearerToken: parsed.hasBearerToken,
      offeredProtocolCount: parsed.offeredProtocolCount,
      allowedMessageTypes: Object.freeze([]),
      responseHeaders: EMPTY_HEADERS,
      error: null,
    };
    return Object.freeze(result);
  }

  const responseHeaders = Object.freeze({
    [SEC_WEBSOCKET_PROTOCOL]: selected.name,
  });

  const result: HandshakeSuccess = {
    accepted: true,
    statusCode: 101,
    subprotocol: selected.name,
    hasSubprotocol: true,
    token: parsed.token,
    hasBearerToken: parsed.hasBearerToken,
    offeredProtocolCount: parsed.offeredProtocolCount,
    allowedMessageTypes: selected.messageTypes,
    responseHeaders,
    error: null,
  };
  return Object.freeze(result);
}

function handshakeFailure(
  error: NegotiationErrorInfo,
  offeredProtocolCount: number,
  hasBearerToken: boolean,
): HandshakeFailure {
  const result: HandshakeFailure = {
    accepted: false,
    statusCode: 400,
    subprotocol: null,
    hasSubprotocol: false,
    token: null,
    hasBearerToken,
    offeredProtocolCount,
    allowedMessageTypes: EMPTY_MESSAGE_TYPES,
    responseHeaders: EMPTY_HEADERS,
    error,
  };
  return Object.freeze(result);
}

function parsedHeaderFailure(
  code: NegotiationErrorCode,
  message: string,
  hasBearerToken: boolean,
  offeredProtocolCount: number,
): ParsedSubprotocolHeaderFailure {
  const result: ParsedSubprotocolHeaderFailure = {
    ok: false,
    error: makeNegotiationError(code, message),
    hasBearerToken,
    offeredProtocolCount,
  };
  return Object.freeze(result);
}

function makeNegotiationError(
  code: NegotiationErrorCode,
  message: string,
): NegotiationErrorInfo {
  const error: NegotiationErrorInfo = { code, message, statusCode: 400 };
  return Object.freeze(error);
}

function emitSafeHandshakeLog(
  logger: HandshakeLogger | undefined,
  result: HandshakeResult,
): void {
  if (logger === undefined) {
    return;
  }

  try {
    logger(handshakeLogRecord(result));
  } catch {
    // Logging must not change handshake or message-delivery decisions.
  }
}

function joinHeaderValue(header: HandshakeHeader): string | null {
  if (header === null || header === undefined) {
    return null;
  }
  if (typeof header === "string") {
    return header;
  }
  if (Array.isArray(header)) {
    return header.join(",");
  }

  throw new TypeError(
    "The Sec-WebSocket-Protocol header must be a string, string array, or undefined.",
  );
}

function normalizeServerProtocols(
  input: SubprotocolInput,
): readonly NormalizedSubprotocolRule[] {
  const rawEntries = toRawServerEntries(input);
  const seen = new Set<string>();
  const entries: NormalizedSubprotocolRule[] = [];

  for (const [name, messageTypes] of rawEntries) {
    if (!isSubprotocolToken(name)) {
      throw new TypeError(
        "Every server subprotocol name must be an RFC 6455 token.",
      );
    }
    if (BEARER_NAMESPACE_RE.test(name)) {
      throw new TypeError(
        "Server subprotocol names must not use the bearer namespace.",
      );
    }
    if (seen.has(name)) {
      throw new TypeError("Server subprotocol names must be unique.");
    }
    seen.add(name);

    const normalizedTypes = normalizeMessageTypes(messageTypes);
    entries.push(
      Object.freeze({
        name,
        messageTypes: normalizedTypes,
      }),
    );
  }

  return Object.freeze(entries);
}

function toRawServerEntries(
  input: SubprotocolInput,
): readonly (readonly [string, readonly string[] | undefined])[] {
  if (input instanceof SubprotocolPolicy) {
    return input.entries.map(
      (entry) => [entry.name, entry.messageTypes] as const,
    );
  }

  if (Array.isArray(input)) {
    return input.map((entry) => {
      if (entry === null || typeof entry !== "object") {
        throw new TypeError("Server subprotocol entries must be objects.");
      }
      return [entry.name, entry.messageTypes] as const;
    });
  }

  if (input instanceof Map) {
    return Array.from(input.entries());
  }

  if (input !== null && typeof input === "object") {
    return Object.entries(input);
  }

  throw new TypeError(
    "Server subprotocols must be an array, object map, or Map.",
  );
}

function normalizeMessageTypes(
  messageTypes: readonly string[] | undefined,
): readonly string[] {
  if (messageTypes === undefined) {
    return Object.freeze([]);
  }
  if (!Array.isArray(messageTypes)) {
    throw new TypeError("Server messageTypes must be an array.");
  }

  const uniqueTypes: string[] = [];
  const seen = new Set<string>();
  for (const messageType of messageTypes) {
    if (typeof messageType !== "string" || messageType.length === 0) {
      throw new TypeError("Server message types must be non-empty strings.");
    }
    if (!seen.has(messageType)) {
      seen.add(messageType);
      uniqueTypes.push(messageType);
    }
  }

  return Object.freeze(uniqueTypes);
}
