import {
  assertCreateSessionInput,
  canonicalizeCertificate,
  canonicalizeConnectionDescriptor,
  canonicalizeEstablishment,
  canonicalizeOrigin,
  canonicalizeSessionUrl,
  certificateCovers,
  copyPayload,
  stableKey,
  type CongestionSample,
  type CongestionStats,
  type ConnectionDescriptor,
  type ConnectionHandle,
  type ConnectionIdentity,
  type ConnectionState,
  type ConnectionSummary,
  type CreateSessionInput,
  type DatagramEnvelope,
  type FreezeReason,
  type OriginFrameAcceptance,
  type OriginFrameInput,
  type QuicGeneration,
  type SessionEstablishment,
  type SessionHandle,
  type SessionSummary,
  type StreamGrant,
  type VerifiedServerCertificate,
} from "./canonical.ts";
import {
  CertificateMismatchError,
  ConnectionClosedError,
  ConnectionConflictError,
  ConnectionNotCurrentError,
  DuplicateQuicGenerationError,
  DuplicateSessionIdError,
  InvalidConnectionDescriptorError,
  InvalidSessionRequestError,
  OriginFrameRejectedError,
  OriginNotAuthorizedError,
  QuotaExceededError,
  SessionAllocationFrozenError,
  SessionClosedError,
  StaleQuicGenerationError,
} from "./errors.ts";

export interface SessionEstablishmentContext {
  readonly connection: ConnectionHandle;
  readonly origin: string;
  readonly canonicalUrl: string;
  readonly earlyData: boolean;
}

export interface WebTransportSessionPoolOptions {
  readonly now?: () => number;
  /**
   * Browser network stack hook that performs the WebTransport CONNECT and
   * returns the server's session establishment. Tests can return a deferred
   * promise to exercise GOAWAY/certificate/network-switch races.
   */
  readonly establishSession?: (
    context: SessionEstablishmentContext,
  ) => SessionEstablishment | Promise<SessionEstablishment>;
  /** Optional cleanup hook when the CONNECT is discarded before commit. */
  readonly abortSession?: (context: SessionEstablishmentContext) => void;
  /** Bounds the per-session inbound datagram queue (default 1024). */
  readonly maxInboundDatagramsPerSession?: number;
}

interface StoredSession {
  readonly internalId: string;
  readonly handle: SessionHandle;
  readonly connection: StoredConnection;
  readonly origin: string;
  readonly canonicalUrl: string;
  readonly sessionId: string;
  readonly draft: string;
  readonly version: string;
  readonly earlyData: boolean;
  readonly streamLimit: number;
  readonly datagramLimit: number;
  readonly activeStreams: Set<number>;
  readonly datagramQueue: Uint8Array[];
  datagramsUsed: number;
  state: "open" | "closed";
  readonly createdAt: number;
}

interface StoredConnection {
  readonly id: string;
  readonly handle: ConnectionHandle;
  readonly identity: ConnectionIdentity;
  readonly initialOrigin: string;
  readonly transportKey: string;
  readonly descriptorKey: string;
  state: ConnectionState;
  readonly freezeReasons: Set<FreezeReason>;
  readonly activeSessions: Map<string, StoredSession>;
  readonly allSessionIds: Set<string>;
  readonly streamToSession: Map<number, StoredSession>;
  readonly originFrameOrigins: Set<string>;
  nextStreamId: number;
  transportRetired: boolean;
  zeroRttRejected: boolean;
  originFrameAccepted: boolean;
  congestionSamples: number;
  lastCongestionSample: CongestionSample;
  readonly createdAt: number;
}

interface GoAwayEvent {
  readonly generation?: QuicGeneration;
  readonly lastStreamId?: number;
}

interface CertificateUpdateEvent {
  readonly generation?: QuicGeneration;
  readonly newCertificate?: VerifiedServerCertificate;
}

interface NetworkSwitchEvent {
  readonly generation?: QuicGeneration;
  readonly toNetworkPartitionKey?: string;
}

const EMPTY_DATAGRAM_QUEUE = Object.freeze([]) as readonly Uint8Array[];
const CONGESTION_FIELDS = [
  "smoothedRttMs",
  "packetsInFlight",
  "congestionWindowBytes",
  "lostPackets",
  "sentPackets",
] as const;

export class WebTransportSessionPool {
  readonly #now: () => number;
  readonly #establishSession:
    | ((context: SessionEstablishmentContext) => SessionEstablishment | Promise<SessionEstablishment>)
    | undefined;
  readonly #abortSession: ((context: SessionEstablishmentContext) => void) | undefined;
  readonly #maxInboundDatagramsPerSession: number;

  readonly #connections = new Map<string, StoredConnection>();
  readonly #connectionsBySlot = new Map<string, Set<StoredConnection>>();
  readonly #latestGenerationBySlot = new Map<string, QuicGeneration>();
  readonly #connectionByGeneration = new Map<QuicGeneration, StoredConnection>();
  readonly #sessionHandles = new WeakMap<SessionHandle, StoredSession>();
  readonly #sessionsByGeneration = new Map<QuicGeneration, Map<string, StoredSession>>();

  #nextConnectionId = 1;
  #nextSessionInternalId = 1;
  #nextDefaultServerSessionId = 1;
  #nextGeneration = 1;

  constructor(options: WebTransportSessionPoolOptions = {}) {
    this.#now = options.now ?? (() => Date.now());
    this.#establishSession = options.establishSession;
    this.#abortSession = options.abortSession;
    this.#maxInboundDatagramsPerSession = options.maxInboundDatagramsPerSession ?? 1024;
    if (
      !Number.isSafeInteger(this.#maxInboundDatagramsPerSession) ||
      this.#maxInboundDatagramsPerSession < 0
    ) {
      throw new InvalidConnectionDescriptorError("maxInboundDatagramsPerSession must be >= 0");
    }
  }

  /**
   * Registers a verified QUIC connection. The transport key deliberately does
   * not contain an origin: certificate-covered origins may coalesce, but
   * session creation still needs same-origin authority or a validated ORIGIN
   * frame. A newer generation supersedes and immediately retires the old one.
   */
  openConnection(descriptor: ConnectionDescriptor): ConnectionHandle {
    const canonical = canonicalizeConnectionDescriptor(descriptor);
    const latest = this.#latestGenerationBySlot.get(canonical.transportKey);
    const requestedGeneration = canonical.quicGeneration;
    let generation: QuicGeneration;

    if (requestedGeneration !== undefined) {
      if (latest !== undefined && requestedGeneration < latest) {
        throw new StaleQuicGenerationError(requestedGeneration, latest);
      }
      const existing = this.#connectionByGeneration.get(requestedGeneration);
      if (existing !== undefined) {
        const sameDescriptor =
          existing.transportKey === canonical.transportKey &&
          existing.initialOrigin === canonical.initialOrigin;
        if (sameDescriptor && existing.state === "active") {
          return existing.handle;
        }
        if (sameDescriptor) {
          throw new ConnectionConflictError(
            `QUIC generation ${requestedGeneration} is no longer active`,
          );
        }
        throw new DuplicateQuicGenerationError(requestedGeneration);
      }
      if (latest !== undefined && requestedGeneration === latest) {
        throw new ConnectionConflictError(
          `QUIC generation ${requestedGeneration} is already current for this transport key`,
        );
      }
      generation = requestedGeneration;
      this.#nextGeneration = Math.max(this.#nextGeneration, generation + 1);
    } else {
      generation = Math.max(latest === undefined ? 1 : latest + 1, this.#nextGeneration);
      while (this.#connectionByGeneration.has(generation)) {
        generation += 1;
      }
      this.#nextGeneration = generation + 1;
    }

    const identity: ConnectionIdentity = Object.freeze({
      ...canonical.identity,
      quicGeneration: generation,
    });
    const descriptorKey = stableKey([
      canonical.transportKey,
      String(generation),
      canonical.initialOrigin,
    ]);
    const id = `conn-${this.#nextConnectionId++}`;
    const handle: ConnectionHandle = Object.freeze({
      id,
      generation,
      initialOrigin: canonical.initialOrigin,
      identity,
    });
    const record: StoredConnection = {
      id,
      handle,
      identity,
      initialOrigin: canonical.initialOrigin,
      transportKey: canonical.transportKey,
      descriptorKey,
      state: "active",
      freezeReasons: new Set<FreezeReason>(),
      activeSessions: new Map<string, StoredSession>(),
      allSessionIds: new Set<string>(),
      streamToSession: new Map<number, StoredSession>(),
      originFrameOrigins: new Set<string>(),
      nextStreamId: 1,
      transportRetired: false,
      zeroRttRejected: false,
      originFrameAccepted: false,
      congestionSamples: 0,
      lastCongestionSample: Object.freeze({}),
      createdAt: this.#now(),
    };

    const slotConnections = this.#connectionsBySlot.get(canonical.transportKey);
    if (slotConnections !== undefined) {
      for (const old of slotConnections) {
        if (old.state === "closed" || old.state === "superseded") {
          continue;
        }
        old.freezeReasons.add("superseded");
        old.state = "superseded";
        old.transportRetired = true;
      }
      slotConnections.add(record);
    } else {
      this.#connectionsBySlot.set(canonical.transportKey, new Set([record]));
    }

    this.#connections.set(id, record);
    this.#connectionByGeneration.set(generation, record);
    this.#latestGenerationBySlot.set(canonical.transportKey, generation);
    return handle;
  }

  getConnection(connectionId: string): ConnectionHandle | undefined {
    return this.#connections.get(connectionId)?.handle;
  }

  /**
   * Creates a server-returned CONNECT session. Authorization, certificate
   * coverage, generation validity, and freeze state are checked both before
   * and after the asynchronous CONNECT so GOAWAY and other races cannot
   * commit an unauthorized session.
   */
  async createSession(input: CreateSessionInput): Promise<SessionHandle> {
    assertCreateSessionInput(input);
    const connection = this.#requireConnection(input.connectionId);
    const request = canonicalizeSessionUrl(input.url);
    if (input.origin !== undefined) {
      const assertedOrigin = canonicalizeOrigin(input.origin, "invalid_session_request");
      if (assertedOrigin !== request.origin) {
        throw new InvalidSessionRequestError(
          `asserted origin ${assertedOrigin} does not match URL origin ${request.origin}`,
        );
      }
    }
    this.#assertCertificateCoverage(connection, request.origin);
    const requestedEarlyData = input.earlyData === true;
    this.#assertSessionMayStart(connection, requestedEarlyData);
    this.#assertOriginAuthorized(connection, request.origin);

    const context: SessionEstablishmentContext = Object.freeze({
      connection: connection.handle,
      origin: request.origin,
      canonicalUrl: request.url,
      earlyData: requestedEarlyData,
    });

    let serverResult: SessionEstablishment;
    try {
      if (input.establishment !== undefined) {
        serverResult = input.establishment;
      } else if (this.#establishSession !== undefined) {
        serverResult = await this.#establishSession(context);
      } else {
        serverResult = this.#defaultEstablishment(connection);
      }
    } catch (error) {
      this.#abort(context);
      throw error;
    }

    // Yield even for a synchronously supplied establishment. This models the
    // CONNECT round trip and makes deterministic race tests possible.
    serverResult = await Promise.resolve(serverResult);
    let established: ReturnType<typeof canonicalizeEstablishment>;
    try {
      established = canonicalizeEstablishment(serverResult);
    } catch (error) {
      this.#abort(context);
      throw error;
    }

    const earlyData = requestedEarlyData || established.earlyData;
    const freezeReasons = this.#allocationFreezeReasons(connection, earlyData);
    if (freezeReasons.length > 0) {
      this.#abort(context);
      throw new SessionAllocationFrozenError(freezeReasons);
    }
    if (connection.allSessionIds.has(established.sessionId)) {
      this.#abort(context);
      throw new DuplicateSessionIdError(established.sessionId);
    }
    // Re-check authorization after the asynchronous gap. Certificate coverage
    // is immutable per connection, but a generation could have been retired.
    this.#assertOriginAuthorized(connection, request.origin);

    const internalId = `sess-${this.#nextSessionInternalId++}`;
    const handle: SessionHandle = Object.freeze({
      id: internalId,
      connectionId: connection.id,
      quicGeneration: connection.identity.quicGeneration,
      origin: request.origin,
      canonicalUrl: request.url,
      sessionId: established.sessionId,
      draft: established.draft,
      version: established.version,
    });
    const session: StoredSession = {
      internalId,
      handle,
      connection,
      origin: request.origin,
      canonicalUrl: request.url,
      sessionId: established.sessionId,
      draft: established.draft,
      version: established.version,
      earlyData,
      streamLimit: established.streamLimit,
      datagramLimit: established.datagramLimit,
      activeStreams: new Set<number>(),
      datagramQueue: [],
      datagramsUsed: 0,
      state: "open",
      createdAt: this.#now(),
    };

    const generation = connection.identity.quicGeneration;
    let generationSessions = this.#sessionsByGeneration.get(generation);
    if (generationSessions === undefined) {
      generationSessions = new Map<string, StoredSession>();
      this.#sessionsByGeneration.set(generation, generationSessions);
    }
    generationSessions.set(session.sessionId, session);
    connection.activeSessions.set(session.sessionId, session);
    connection.allSessionIds.add(session.sessionId);
    this.#sessionHandles.set(handle, session);
    return handle;
  }

  /**
   * Accepts a server ORIGIN frame from the authenticated HTTP/3 control stream.
   * The whole frame is rejected atomically if any claimed origin is not covered
   * by the verified certificate or if it is stale/forged.
   */
  receiveOriginFrame(connectionId: string, frame: OriginFrameInput): OriginFrameAcceptance {
    const connection = this.#requireConnection(connectionId);
    if (
      connection.state === "closed" ||
      connection.state === "superseded" ||
      connection.transportRetired
    ) {
      throw new OriginFrameRejectedError("connection generation is no longer active");
    }
    if (frame === null || typeof frame !== "object") {
      throw new OriginFrameRejectedError("ORIGIN frame must be an object");
    }
    if (frame.source !== "http3-control-stream") {
      throw new OriginFrameRejectedError(
        "ORIGIN frame did not come from the authenticated HTTP/3 control stream",
        "origin_frame_trust_failure",
      );
    }
    if (
      typeof frame.generation !== "number" ||
      !Number.isSafeInteger(frame.generation) ||
      frame.generation !== connection.identity.quicGeneration
    ) {
      throw new OriginFrameRejectedError("ORIGIN frame is from a stale QUIC generation");
    }
    if (!Array.isArray(frame.origins) || frame.origins.length === 0) {
      throw new OriginFrameRejectedError("ORIGIN frame must contain at least one origin");
    }

    const accepted: string[] = [];
    try {
      for (const rawOrigin of frame.origins) {
        const origin = canonicalizeOrigin(rawOrigin, "invalid_origin_frame");
        if (!certificateCovers(connection.identity.certificate, origin)) {
          throw new OriginFrameRejectedError(
            `ORIGIN frame claims ${origin}, which is not covered by the verified certificate`,
          );
        }
        accepted.push(origin);
      }
    } catch (error) {
      if (error instanceof OriginFrameRejectedError) {
        throw error;
      }
      throw new OriginFrameRejectedError(
        error instanceof Error ? error.message : "invalid ORIGIN frame",
      );
    }

    for (const origin of accepted) {
      connection.originFrameOrigins.add(origin);
    }
    connection.originFrameAccepted = true;
    return Object.freeze({
      accepted: Object.freeze([...new Set(accepted)].sort()),
      authorizedOrigins: Object.freeze(this.#authorizedOrigins(connection)),
    });
  }

  /** Freezes new allocations. Existing sessions may continue. */
  handleGoAway(connectionId: string, event: GoAwayEvent = {}): boolean {
    const connection = this.#connections.get(connectionId);
    if (connection === undefined || connection.state === "closed") {
      return false;
    }
    if (event.generation !== undefined && event.generation !== connection.identity.quicGeneration) {
      return false;
    }
    connection.freezeReasons.add("goaway");
    if (connection.state !== "closed" && connection.state !== "superseded") {
      connection.state = "draining";
    }
    return true;
  }

  /**
   * A certificate update invalidates the old verified identity for new
   * allocations. The existing transport identity is immutable; a new
   * generation/connection must be opened with the new certificate identity.
   */
  handleCertificateUpdate(connectionId: string, event: CertificateUpdateEvent = {}): boolean {
    const connection = this.#connections.get(connectionId);
    if (connection === undefined || connection.state === "closed") {
      return false;
    }
    if (event.generation !== undefined && event.generation !== connection.identity.quicGeneration) {
      return false;
    }
    // Fail closed even if the event's certificate payload is malformed.
    connection.freezeReasons.add("certificate_update");
    if (connection.state !== "closed" && connection.state !== "superseded") {
      connection.state = "draining";
    }
    if (event.newCertificate !== undefined) {
      const newCertificate = canonicalizeCertificate(event.newCertificate);
      if (!certificateCovers(newCertificate, connection.initialOrigin)) {
        throw new CertificateMismatchError(
          connection.initialOrigin,
          "updated certificate does not cover the connection's verified origin",
        );
      }
    }
    return true;
  }

  /**
   * A network switch retires the old generation immediately: new allocations
   * are frozen and late datagrams for the old generation are dropped.
   */
  handleNetworkSwitch(connectionId: string, event: NetworkSwitchEvent = {}): boolean {
    const connection = this.#connections.get(connectionId);
    if (connection === undefined || connection.state === "closed") {
      return false;
    }
    if (event.generation !== undefined && event.generation !== connection.identity.quicGeneration) {
      return false;
    }
    // The network path changed; freeze first and validate metadata afterwards.
    connection.freezeReasons.add("network_switch");
    connection.transportRetired = true;
    if (connection.state !== "closed" && connection.state !== "superseded") {
      connection.state = "draining";
    }
    if (
      event.toNetworkPartitionKey !== undefined &&
      (typeof event.toNetworkPartitionKey !== "string" ||
        event.toNetworkPartitionKey.trim().length === 0)
    ) {
      throw new InvalidConnectionDescriptorError("new network partition key must be a non-empty string");
    }
    return true;
  }

  /**
   * A rejected 0-RTT attempt freezes all new allocations and closes sessions
   * provisionally established with early data. 1-RTT sessions continue.
   */
  rejectZeroRtt(connectionId: string, event: GoAwayEvent = {}): number {
    const connection = this.#connections.get(connectionId);
    if (connection === undefined || connection.state === "closed") {
      return 0;
    }
    if (event.generation !== undefined && event.generation !== connection.identity.quicGeneration) {
      return 0;
    }
    connection.freezeReasons.add("zero_rtt_rejection");
    connection.zeroRttRejected = true;
    if (connection.state !== "closed" && connection.state !== "superseded") {
      connection.state = "draining";
    }
    let closed = 0;
    for (const session of [...connection.activeSessions.values()]) {
      if (session.earlyData) {
        this.#closeStoredSession(session);
        closed += 1;
      }
    }
    return closed;
  }

  receiveGoAway(connectionId: string, event: GoAwayEvent = {}): boolean {
    return this.handleGoAway(connectionId, event);
  }

  notifyCertificateUpdate(connectionId: string, event: CertificateUpdateEvent = {}): boolean {
    return this.handleCertificateUpdate(connectionId, event);
  }

  notifyNetworkSwitch(connectionId: string, event: NetworkSwitchEvent = {}): boolean {
    return this.handleNetworkSwitch(connectionId, event);
  }

  handleZeroRttRejection(connectionId: string, event: GoAwayEvent = {}): number {
    return this.rejectZeroRtt(connectionId, event);
  }

  /** Routes a delayed datagram by (current generation, session ID). */
  deliverDatagram(
    inputOrGeneration: DatagramEnvelope | QuicGeneration,
    sessionIdArg?: string,
    payloadArg?: Uint8Array | string,
  ): boolean {
    let generation: QuicGeneration;
    let sessionId: string;
    let payload: Uint8Array | string;
    if (typeof inputOrGeneration === "number") {
      generation = inputOrGeneration;
      if (typeof sessionIdArg !== "string") {
        return false;
      }
      sessionId = sessionIdArg;
      payload = payloadArg as Uint8Array | string;
    } else {
      generation = inputOrGeneration.quicGeneration;
      sessionId = inputOrGeneration.sessionId;
      payload = inputOrGeneration.payload;
    }
    if (
      !Number.isSafeInteger(generation) ||
      generation < 0 ||
      typeof sessionId !== "string" ||
      sessionId.length === 0
    ) {
      return false;
    }
    const connection = this.#connectionByGeneration.get(generation);
    if (connection === undefined || !this.#isGenerationCurrent(connection)) {
      return false;
    }
    const sessions = this.#sessionsByGeneration.get(generation);
    const session = sessions?.get(sessionId);
    if (
      session === undefined ||
      session.connection !== connection ||
      session.state !== "open"
    ) {
      return false;
    }
    if (session.datagramQueue.length >= this.#maxInboundDatagramsPerSession) {
      return false;
    }
    session.datagramQueue.push(copyPayload(payload));
    return true;
  }

  pollDatagrams(handle: SessionHandle): readonly Uint8Array[] {
    const session = this.#resolveSession(handle);
    if (session.state !== "open" || session.datagramQueue.length === 0) {
      return EMPTY_DATAGRAM_QUEUE;
    }
    return Object.freeze(session.datagramQueue.splice(0).map((payload) => payload.slice()));
  }

  sendDatagram(handle: SessionHandle, payload: Uint8Array | string): DatagramEnvelope {
    const session = this.#requireOpenSession(handle);
    this.#assertConnectionUsableForSession(session);
    if (session.datagramsUsed >= session.datagramLimit) {
      throw new QuotaExceededError("datagram", session.datagramLimit);
    }
    session.datagramsUsed += 1;
    return Object.freeze({
      quicGeneration: session.connection.identity.quicGeneration,
      sessionId: session.sessionId,
      payload: copyPayload(payload),
    });
  }

  openStream(handle: SessionHandle): StreamGrant {
    const session = this.#requireOpenSession(handle);
    this.#assertConnectionUsableForSession(session);
    if (session.activeStreams.size >= session.streamLimit) {
      throw new QuotaExceededError("stream", session.streamLimit);
    }
    const streamId = session.connection.nextStreamId;
    session.connection.nextStreamId += 1;
    session.connection.streamToSession.set(streamId, session);
    session.activeStreams.add(streamId);
    return Object.freeze({
      quicGeneration: session.connection.identity.quicGeneration,
      sessionId: session.sessionId,
      streamId,
    });
  }

  closeStream(handle: SessionHandle, streamId: number): boolean {
    const session = this.#resolveSession(handle);
    if (session.state !== "open" || !Number.isSafeInteger(streamId)) {
      return false;
    }
    if (!session.activeStreams.has(streamId)) {
      return false;
    }
    session.activeStreams.delete(streamId);
    if (session.connection.streamToSession.get(streamId) === session) {
      session.connection.streamToSession.delete(streamId);
    }
    return true;
  }

  closeSession(handle: SessionHandle): boolean {
    const session = this.#resolveSession(handle);
    if (session.state === "closed") {
      return false;
    }
    this.#closeStoredSession(session);
    return true;
  }

  closeConnection(connectionId: string): number {
    const connection = this.#connections.get(connectionId);
    if (connection === undefined || connection.state === "closed") {
      return 0;
    }
    connection.freezeReasons.add("closed");
    connection.state = "closed";
    connection.transportRetired = true;
    let closed = 0;
    for (const session of [...connection.activeSessions.values()]) {
      this.#closeStoredSession(session);
      closed += 1;
    }
    this.#connectionByGeneration.delete(connection.identity.quicGeneration);
    return closed;
  }

  getSessionSummary(handle: SessionHandle): SessionSummary {
    const session = this.#resolveSession(handle);
    return Object.freeze({
      state: session.state,
      origin: session.origin,
      canonicalUrl: session.canonicalUrl,
      sessionId: session.sessionId,
      draft: session.draft,
      version: session.version,
      earlyData: session.earlyData,
      streamQuota: Object.freeze({
        limit: session.streamLimit,
        used: session.activeStreams.size,
      }),
      datagramQuota: Object.freeze({
        limit: session.datagramLimit,
        used: session.datagramsUsed,
      }),
      streamIds: Object.freeze([...session.activeStreams].sort((left, right) => left - right)),
      queuedDatagrams: session.datagramQueue.length,
    });
  }

  getConnectionSummary(connectionId: string): ConnectionSummary | undefined {
    const connection = this.#connections.get(connectionId);
    if (connection === undefined) {
      return undefined;
    }
    return Object.freeze({
      id: connection.id,
      generation: connection.identity.quicGeneration,
      state: connection.state,
      freezeReasons: Object.freeze([...connection.freezeReasons].sort()),
      generationCurrent: this.#isGenerationCurrent(connection),
      activeSessions: connection.activeSessions.size,
      authorizedOrigins: Object.freeze(this.#authorizedOrigins(connection)),
      originFrameAccepted: connection.originFrameAccepted,
      zeroRttRejected: connection.zeroRttRejected,
      congestionSamples: connection.congestionSamples,
    });
  }

  /**
   * Congestion statistics are connection-scoped bookkeeping and may be
   * intentionally shared by coalesced origins.
   */
  recordCongestion(handle: SessionHandle, sample: CongestionSample): void {
    const session = this.#resolveSession(handle);
    if (session.state !== "open") {
      throw new SessionClosedError();
    }
    if (sample === null || typeof sample !== "object") {
      throw new InvalidSessionRequestError("congestion sample must be an object");
    }
    const next: { [key: string]: number } = {};
    for (const field of CONGESTION_FIELDS) {
      const value = sample[field];
      if (value === undefined) {
        continue;
      }
      if (typeof value !== "number" || !Number.isFinite(value) || value < 0) {
        throw new InvalidSessionRequestError(`congestion sample ${field} must be a finite non-negative number`);
      }
      next[field] = value;
    }
    const connection = session.connection;
    connection.lastCongestionSample = Object.freeze({
      ...connection.lastCongestionSample,
      ...next,
    });
    connection.congestionSamples += 1;
  }

  getCongestionStats(handle: SessionHandle): CongestionStats {
    const session = this.#resolveSession(handle);
    return Object.freeze({
      samples: session.connection.congestionSamples,
      last: Object.freeze({ ...session.connection.lastCongestionSample }),
    });
  }

  #defaultEstablishment(connection: StoredConnection): SessionEstablishment {
    return {
      sessionId: `srv-${connection.id}-${this.#nextDefaultServerSessionId++}`,
      draft: "draft-ietf-webtransport-http3-13",
      version: "13",
      streamLimit: 16,
      datagramLimit: 256,
    };
  }

  #abort(context: SessionEstablishmentContext): void {
    try {
      this.#abortSession?.(context);
    } catch {
      // Abort cleanup must not mask the allocation failure.
    }
  }

  #requireConnection(connectionId: string): StoredConnection {
    if (typeof connectionId !== "string" || connectionId.length === 0) {
      throw new InvalidSessionRequestError("connectionId must be a non-empty string");
    }
    const connection = this.#connections.get(connectionId);
    if (connection === undefined) {
      throw new ConnectionClosedError(`unknown connection ${JSON.stringify(connectionId)}`);
    }
    return connection;
  }

  #resolveSession(handle: SessionHandle): StoredSession {
    const session = this.#sessionHandles.get(handle);
    if (session === undefined) {
      throw new SessionClosedError("unknown or foreign session handle");
    }
    return session;
  }

  #requireOpenSession(handle: SessionHandle): StoredSession {
    const session = this.#resolveSession(handle);
    if (session.state !== "open") {
      throw new SessionClosedError();
    }
    return session;
  }

  #assertCertificateCoverage(connection: StoredConnection, origin: string): void {
    if (!certificateCovers(connection.identity.certificate, origin)) {
      throw new CertificateMismatchError(origin);
    }
  }

  #assertOriginAuthorized(connection: StoredConnection, origin: string): void {
    if (origin === connection.initialOrigin) {
      return;
    }
    if (!connection.originFrameOrigins.has(origin)) {
      throw new OriginNotAuthorizedError(origin);
    }
  }

  #allocationFreezeReasons(connection: StoredConnection, earlyData: boolean): FreezeReason[] {
    const reasons = new Set<FreezeReason>(connection.freezeReasons);
    if (connection.state === "closed") {
      reasons.add("closed");
    }
    if (connection.zeroRttRejected && earlyData) {
      reasons.add("zero_rtt_rejection");
    }
    if (!this.#isGenerationCurrent(connection)) {
      if (connection.state === "superseded") {
        reasons.add("superseded");
      }
    }
    return [...reasons].sort();
  }

  #assertSessionMayStart(connection: StoredConnection, earlyData: boolean): void {
    const reasons = this.#allocationFreezeReasons(connection, earlyData);
    if (reasons.length > 0) {
      throw new SessionAllocationFrozenError(reasons);
    }
  }

  #isGenerationCurrent(connection: StoredConnection): boolean {
    return (
      connection.state !== "closed" &&
      !connection.transportRetired &&
      this.#latestGenerationBySlot.get(connection.transportKey) ===
        connection.identity.quicGeneration
    );
  }

  #assertConnectionUsableForSession(session: StoredSession): void {
    if (session.connection.state === "closed") {
      throw new ConnectionClosedError();
    }
    if (!this.#isGenerationCurrent(session.connection)) {
      throw new ConnectionNotCurrentError();
    }
  }

  #authorizedOrigins(connection: StoredConnection): string[] {
    return [...new Set([connection.initialOrigin, ...connection.originFrameOrigins])].sort();
  }

  #closeStoredSession(session: StoredSession): void {
    if (session.state === "closed") {
      return;
    }
    session.state = "closed";
    session.datagramQueue.length = 0;
    for (const streamId of session.activeStreams) {
      if (session.connection.streamToSession.get(streamId) === session) {
        session.connection.streamToSession.delete(streamId);
      }
    }
    session.activeStreams.clear();
    if (session.connection.activeSessions.get(session.sessionId) === session) {
      session.connection.activeSessions.delete(session.sessionId);
    }
    const generationSessions = this.#sessionsByGeneration.get(
      session.connection.identity.quicGeneration,
    );
    if (generationSessions?.get(session.sessionId) === session) {
      generationSessions.delete(session.sessionId);
    }
  }
}
