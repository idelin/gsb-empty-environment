import {
  isOperationAllowed,
  type PageOperation,
  type SecurityConfig,
} from "./config.ts";
import {
  HARD_LIMITS,
  boundedInteger,
  boundedString,
  byteLength,
  deepFreeze,
  exactRecord,
  hasControlCharacters,
  isOriginInList,
  ownDataProperty,
  parseCanonicalOrigin,
  randomId,
  reject,
  ValidationError,
} from "./security.ts";

export const BRIDGE_HELLO_TYPE = "extension:bridge-hello";
export const BRIDGE_READY_TYPE = "extension:bridge-ready";
export const BRIDGE_REQUEST_TYPE = "extension:bridge-request";
export const BRIDGE_RESPONSE_TYPE = "extension:bridge-response";
export const BRIDGE_PROTOCOL_VERSION = 1;

const HELLO_KEYS = Object.freeze([
  "type",
  "protocolVersion",
  "requestId",
] as const);

const REQUEST_KEYS = Object.freeze([
  "type",
  "protocolVersion",
  "sessionId",
  "requestId",
  "operation",
  "targetOrigin",
  "body",
] as const);

const DISPATCH_RESULT_KEYS = Object.freeze(["ok", "value"] as const);
const REQUEST_ID_PATTERN = /^[A-Za-z0-9._:-]{1,96}$/;
const SESSION_ID_PATTERN = /^[a-f0-9]{16,128}$/;

export interface ContentWindowLike {
  readonly location: Readonly<{ origin: string }>;
  addEventListener(
    type: "message",
    listener: (event: MessageEvent) => void,
  ): void;
  removeEventListener(
    type: "message",
    listener: (event: MessageEvent) => void,
  ): void;
  postMessage(
    message: unknown,
    targetOrigin: string,
    transfer?: Transferable[],
  ): void;
}

export interface ValidatedPageRequest {
  readonly type: typeof BRIDGE_REQUEST_TYPE;
  readonly protocolVersion: 1;
  readonly sessionId: string;
  readonly requestId: string;
  readonly operation: PageOperation;
  readonly targetOrigin: string;
  readonly body: string;
}

export interface PrivilegedDispatchResult {
  readonly ok: boolean;
  readonly value: string;
}

export type PrivilegedDispatcher = (
  request: Readonly<ValidatedPageRequest>,
) => PrivilegedDispatchResult | Promise<PrivilegedDispatchResult>;

export interface OneTimePageBridgeOptions {
  readonly hostWindow: ContentWindowLike;
  readonly expectedOrigin: string;
  readonly config: SecurityConfig;
  readonly dispatch: PrivilegedDispatcher;
  readonly now?: () => number;
}

interface ActiveSession {
  readonly id: string;
  readonly port: MessagePort;
  used: boolean;
  closed: boolean;
  idleTimer: ReturnType<typeof setTimeout> | undefined;
}

function unrefTimer(timer: ReturnType<typeof setTimeout>): void {
  const candidate = timer as unknown as { unref?: () => void };
  candidate.unref?.();
}

function readRequired(
  record: Record<string, unknown>,
  key: string,
  label: string,
): unknown {
  return ownDataProperty(record, key, label);
}

function parseHello(data: unknown): Readonly<{ requestId: string }> {
  const record = exactRecord(data, HELLO_KEYS, "bridgeHello");
  const type = boundedString(
    readRequired(record, "type", "bridgeHello"),
    "bridgeHello.type",
    1,
    64,
  );
  if (type !== BRIDGE_HELLO_TYPE) {
    reject("not_allowed", "unexpected bridge hello type");
  }
  const protocolVersion = boundedInteger(
    readRequired(record, "protocolVersion", "bridgeHello"),
    "bridgeHello.protocolVersion",
    1,
    BRIDGE_PROTOCOL_VERSION,
  );
  if (protocolVersion !== BRIDGE_PROTOCOL_VERSION) {
    reject("invalid_value", "unsupported bridge protocol");
  }
  const requestId = boundedString(
    readRequired(record, "requestId", "bridgeHello"),
    "bridgeHello.requestId",
    1,
    HARD_LIMITS.maxRequestIdLength,
  );
  if (
    !REQUEST_ID_PATTERN.test(requestId) ||
    hasControlCharacters(requestId)
  ) {
    reject("invalid_value", "bridgeHello.requestId is malformed");
  }
  return Object.freeze({ requestId });
}

function parsePageRequest(
  data: unknown,
  session: ActiveSession,
  config: SecurityConfig,
): Readonly<ValidatedPageRequest> {
  const record = exactRecord(data, REQUEST_KEYS, "pageRequest");
  const type = boundedString(
    readRequired(record, "type", "pageRequest"),
    "pageRequest.type",
    1,
    64,
  );
  if (type !== BRIDGE_REQUEST_TYPE) {
    reject("not_allowed", "unexpected page request type");
  }

  const protocolVersion = boundedInteger(
    readRequired(record, "protocolVersion", "pageRequest"),
    "pageRequest.protocolVersion",
    1,
    BRIDGE_PROTOCOL_VERSION,
  );
  if (protocolVersion !== BRIDGE_PROTOCOL_VERSION) {
    reject("invalid_value", "unsupported page request protocol");
  }

  const sessionId = boundedString(
    readRequired(record, "sessionId", "pageRequest"),
    "pageRequest.sessionId",
    16,
    HARD_LIMITS.maxSessionIdLength,
  );
  if (
    !SESSION_ID_PATTERN.test(sessionId) ||
    sessionId !== session.id
  ) {
    reject("not_allowed", "page request does not belong to this port");
  }

  const requestId = boundedString(
    readRequired(record, "requestId", "pageRequest"),
    "pageRequest.requestId",
    1,
    HARD_LIMITS.maxRequestIdLength,
  );
  if (
    !REQUEST_ID_PATTERN.test(requestId) ||
    hasControlCharacters(requestId)
  ) {
    reject("invalid_value", "pageRequest.requestId is malformed");
  }

  const operationText = boundedString(
    readRequired(record, "operation", "pageRequest"),
    "pageRequest.operation",
    1,
    HARD_LIMITS.maxOperationLength,
  );
  if (!isOperationAllowed(config, operationText)) {
    reject("not_allowed", "pageRequest operation is not enabled");
  }

  const targetOriginText = boundedString(
    readRequired(record, "targetOrigin", "pageRequest"),
    "pageRequest.targetOrigin",
    1,
    HARD_LIMITS.maxOriginLength,
  );
  const targetOrigin = parseCanonicalOrigin(
    targetOriginText,
    "pageRequest.targetOrigin",
  );
  if (targetOrigin !== targetOriginText) {
    reject("invalid_value", "pageRequest.targetOrigin is not canonical");
  }
  if (!isOriginInList(targetOrigin, config.allowedOrigins)) {
    reject("not_allowed", "pageRequest target is not in the manifest policy");
  }

  const body = boundedString(
    readRequired(record, "body", "pageRequest"),
    "pageRequest.body",
    0,
    config.maxPageStringLength,
  );

  const wireLength = byteLength(
    JSON.stringify({
      type,
      protocolVersion,
      sessionId,
      requestId,
      operation: operationText,
      targetOrigin,
      body,
    }),
  );
  if (wireLength > config.maxPageMessageBytes) {
    reject("too_long", "pageRequest exceeds the configured byte limit");
  }

  return deepFreeze({
    type: BRIDGE_REQUEST_TYPE,
    protocolVersion: 1 as const,
    sessionId,
    requestId,
    operation: operationText as PageOperation,
    targetOrigin,
    body,
  });
}

function parseDispatchResult(
  value: unknown,
  config: SecurityConfig,
): PrivilegedDispatchResult {
  const record = exactRecord(value, DISPATCH_RESULT_KEYS, "dispatchResult");
  const ok = readRequired(record, "ok", "dispatchResult");
  if (typeof ok !== "boolean") {
    reject("invalid_type", "dispatchResult.ok must be a boolean");
  }
  const text = boundedString(
    readRequired(record, "value", "dispatchResult"),
    "dispatchResult.value",
    0,
    config.maxPageStringLength,
  );
  return Object.freeze({ ok, value: text });
}

/**
 * A content-script-to-page bridge with these invariants:
 * - only the exact host window and its exact origin may start a session;
 * - each handshake creates a fresh MessageChannel and exactly one request;
 * - the page never supplies the transferred port;
 * - no callback is attached to the page window.
 */
export class OneTimePageBridge {
  readonly #hostWindow: ContentWindowLike;
  readonly #expectedOrigin: string;
  readonly #dispatch: PrivilegedDispatcher;
  readonly #now: () => number;

  #config: SecurityConfig;
  #started = false;
  #active: ActiveSession | undefined;
  #lastHandshakeAt = Number.NEGATIVE_INFINITY;

  readonly #onWindowMessage = (event: MessageEvent): void => {
    this.#handleWindowMessage(event);
  };

  constructor(options: OneTimePageBridgeOptions) {
    this.#hostWindow = options.hostWindow;
    this.#expectedOrigin = parseCanonicalOrigin(
      options.expectedOrigin,
      "expectedOrigin",
    );
    if (this.#hostWindow.location.origin !== this.#expectedOrigin) {
      throw new ValidationError(
        "invalid_value",
        "expectedOrigin must match the host window",
      );
    }
    this.#config = options.config;
    this.#dispatch = options.dispatch;
    this.#now = options.now ?? (() => Date.now());
  }

  get hasActiveSession(): boolean {
    return this.#active !== undefined;
  }

  start(): void {
    if (this.#started) {
      return;
    }
    this.#hostWindow.addEventListener("message", this.#onWindowMessage);
    this.#started = true;
  }

  stop(): void {
    if (this.#started) {
      this.#hostWindow.removeEventListener("message", this.#onWindowMessage);
      this.#started = false;
    }
    this.#closeActive();
  }

  updateConfig(config: SecurityConfig): void {
    this.#config = config;
    this.#closeActive();
  }

  #handleWindowMessage(event: MessageEvent): void {
    try {
      if (event.source !== this.#hostWindow) {
        return;
      }
      if (event.origin !== this.#expectedOrigin) {
        return;
      }
      if (this.#hostWindow.location.origin !== this.#expectedOrigin) {
        this.stop();
        return;
      }
      if (event.ports.length !== 0) {
        return;
      }

      const hello = parseHello(event.data);
      const now = this.#now();
      if (
        now - this.#lastHandshakeAt <
        this.#config.handshakeCooldownMs
      ) {
        return;
      }
      this.#lastHandshakeAt = now;
      this.#acceptHandshake(hello.requestId);
    } catch {
      // A page cannot distinguish validation failures from ignored messages.
    }
  }

  #acceptHandshake(requestId: string): void {
    this.#closeActive();

    const channel = new MessageChannel();
    const session: ActiveSession = {
      id: randomId(16),
      port: channel.port1,
      used: false,
      closed: false,
      idleTimer: undefined,
    };
    this.#active = session;

    channel.port1.onmessage = (event: MessageEvent): void => {
      this.#handlePortMessage(event, session);
    };
    channel.port1.onmessageerror = (): void => {
      this.#closeSession(session);
    };

    this.#armIdleTimer(session);

    try {
      this.#hostWindow.postMessage(
        Object.freeze({
          type: BRIDGE_READY_TYPE,
          protocolVersion: BRIDGE_PROTOCOL_VERSION,
          requestId,
          sessionId: session.id,
        }),
        this.#expectedOrigin,
        [channel.port2],
      );
    } catch {
      this.#closeSession(session);
    }
  }

  #armIdleTimer(session: ActiveSession): void {
    if (session.idleTimer !== undefined) {
      clearTimeout(session.idleTimer);
    }
    session.idleTimer = setTimeout(() => {
      this.#closeSession(session);
    }, this.#config.portIdleTimeoutMs);
    unrefTimer(session.idleTimer);
  }

  #handlePortMessage(event: MessageEvent, session: ActiveSession): void {
    if (
      session.closed ||
      session.used ||
      this.#active !== session
    ) {
      return;
    }
    session.used = true;
    if (session.idleTimer !== undefined) {
      clearTimeout(session.idleTimer);
      session.idleTimer = undefined;
    }

    let request: Readonly<ValidatedPageRequest>;
    try {
      request = parsePageRequest(event.data, session, this.#config);
    } catch {
      this.#closeSession(session);
      return;
    }

    void Promise.resolve()
      .then(() => this.#dispatch(request))
      .then((result) => {
        if (session.closed || this.#active !== session) {
          return;
        }
        let safeResult: PrivilegedDispatchResult;
        try {
          safeResult = parseDispatchResult(result, this.#config);
        } catch {
          safeResult = Object.freeze({
            ok: false,
            value: "dispatch_failed",
          });
        }
        this.#sendResponse(session, request.requestId, safeResult);
      })
      .catch(() => {
        if (session.closed || this.#active !== session) {
          return;
        }
        this.#sendResponse(
          session,
          request.requestId,
          Object.freeze({ ok: false, value: "dispatch_failed" }),
        );
      });
  }

  #sendResponse(
    session: ActiveSession,
    requestId: string,
    result: PrivilegedDispatchResult,
  ): void {
    if (session.closed || this.#active !== session) {
      return;
    }
    try {
      session.port.postMessage(
        Object.freeze({
          type: BRIDGE_RESPONSE_TYPE,
          protocolVersion: BRIDGE_PROTOCOL_VERSION,
          sessionId: session.id,
          requestId,
          ok: result.ok,
          value: result.value,
        }),
      );
    } catch {
      this.#closeSession(session);
      return;
    }
    const closeTimer = setTimeout(() => {
      this.#closeSession(session);
    }, 0);
    unrefTimer(closeTimer);
  }

  #closeActive(): void {
    if (this.#active !== undefined) {
      this.#closeSession(this.#active);
    }
  }

  #closeSession(session: ActiveSession): void {
    if (session.closed) {
      return;
    }
    session.closed = true;
    if (session.idleTimer !== undefined) {
      clearTimeout(session.idleTimer);
      session.idleTimer = undefined;
    }
    session.port.onmessage = null;
    session.port.onmessageerror = null;
    session.port.close();
    if (this.#active === session) {
      this.#active = undefined;
    }
  }
}
