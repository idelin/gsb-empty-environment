export type WebTransportPoolErrorCode =
  | "invalid_connection_descriptor"
  | "invalid_session_request"
  | "invalid_session_establishment"
  | "invalid_origin_frame"
  | "origin_frame_trust_failure"
  | "origin_not_authorized"
  | "certificate_mismatch"
  | "stale_quic_generation"
  | "duplicate_quic_generation"
  | "connection_conflict"
  | "allocation_frozen"
  | "duplicate_session_id"
  | "session_closed"
  | "connection_closed"
  | "connection_not_current"
  | "quota_exceeded";

export class WebTransportPoolError extends Error {
  readonly code: WebTransportPoolErrorCode;

  constructor(code: WebTransportPoolErrorCode, message: string) {
    super(message);
    this.name = "WebTransportPoolError";
    this.code = code;
  }
}

export class InvalidConnectionDescriptorError extends WebTransportPoolError {
  constructor(message: string) {
    super("invalid_connection_descriptor", message);
    this.name = "InvalidConnectionDescriptorError";
  }
}

export class InvalidSessionRequestError extends WebTransportPoolError {
  constructor(message: string) {
    super("invalid_session_request", message);
    this.name = "InvalidSessionRequestError";
  }
}

export class InvalidSessionEstablishmentError extends WebTransportPoolError {
  constructor(message: string) {
    super("invalid_session_establishment", message);
    this.name = "InvalidSessionEstablishmentError";
  }
}

export class OriginFrameRejectedError extends WebTransportPoolError {
  constructor(message: string, code: "invalid_origin_frame" | "origin_frame_trust_failure" = "invalid_origin_frame") {
    super(code, message);
    this.name = "OriginFrameRejectedError";
  }
}

export class OriginNotAuthorizedError extends WebTransportPoolError {
  readonly origin: string;

  constructor(origin: string) {
    super(
      "origin_not_authorized",
      `origin ${origin} is not authorized to open a WebTransport session on this connection`,
    );
    this.name = "OriginNotAuthorizedError";
    this.origin = origin;
  }
}

export class CertificateMismatchError extends WebTransportPoolError {
  readonly origin: string;

  constructor(origin: string, message?: string) {
    super(
      "certificate_mismatch",
      message ?? `verified certificate does not cover origin ${origin}`,
    );
    this.name = "CertificateMismatchError";
    this.origin = origin;
  }
}

export class StaleQuicGenerationError extends WebTransportPoolError {
  readonly generation: number;
  readonly latestGeneration: number;

  constructor(generation: number, latestGeneration: number) {
    super(
      "stale_quic_generation",
      `QUIC generation ${generation} is older than generation ${latestGeneration}`,
    );
    this.name = "StaleQuicGenerationError";
    this.generation = generation;
    this.latestGeneration = latestGeneration;
  }
}

export class DuplicateQuicGenerationError extends WebTransportPoolError {
  readonly generation: number;

  constructor(generation: number) {
    super(
      "duplicate_quic_generation",
      `QUIC generation ${generation} is already assigned to a different connection identity`,
    );
    this.name = "DuplicateQuicGenerationError";
    this.generation = generation;
  }
}

export class ConnectionConflictError extends WebTransportPoolError {
  constructor(message: string) {
    super("connection_conflict", message);
    this.name = "ConnectionConflictError";
  }
}

export class SessionAllocationFrozenError extends WebTransportPoolError {
  readonly reasons: readonly string[];

  constructor(reasons: readonly string[]) {
    super(
      "allocation_frozen",
      reasons.length > 0
        ? `new WebTransport session allocation is frozen (${reasons.join(", ")})`
        : "new WebTransport session allocation is frozen",
    );
    this.name = "SessionAllocationFrozenError";
    this.reasons = Object.freeze([...reasons].sort());
  }
}

export class DuplicateSessionIdError extends WebTransportPoolError {
  readonly sessionId: string;

  constructor(sessionId: string) {
    super(
      "duplicate_session_id",
      `session ID ${JSON.stringify(sessionId)} was already used on this connection`,
    );
    this.name = "DuplicateSessionIdError";
    this.sessionId = sessionId;
  }
}

export class SessionClosedError extends WebTransportPoolError {
  constructor(message = "WebTransport session is closed") {
    super("session_closed", message);
    this.name = "SessionClosedError";
  }
}

export class ConnectionClosedError extends WebTransportPoolError {
  constructor(message = "QUIC connection is closed") {
    super("connection_closed", message);
    this.name = "ConnectionClosedError";
  }
}

export class ConnectionNotCurrentError extends WebTransportPoolError {
  constructor(message = "QUIC connection is no longer the current generation") {
    super("connection_not_current", message);
    this.name = "ConnectionNotCurrentError";
  }
}

export class QuotaExceededError extends WebTransportPoolError {
  readonly resource: "stream" | "datagram";

  constructor(resource: "stream" | "datagram", limit: number) {
    super("quota_exceeded", `${resource} quota of ${limit} is exhausted`);
    this.name = "QuotaExceededError";
    this.resource = resource;
  }
}
