import { Session, SessionHandle } from "./session.ts";
import type {
  CertIdentity,
  CongestionStats,
  ConnectionKey,
  ConnectionKeyParts,
  ConnectionState,
  FreezeReason,
  OriginFrame,
} from "./types.ts";
import { certCoversHost, canonicalizeOrigin } from "./types.ts";

/**
 * A pooled HTTP/3 connection able to carry multiple WebTransport sessions.
 *
 * Shared across origins on this connection (and nothing else):
 *   - the session ID allocator,
 *   - the QUIC stream-ID -> session-ID map,
 *   - congestion statistics.
 * All other application state is per-session and origin-isolated.
 */
export class PooledConnection {
  readonly key: ConnectionKey;
  readonly keyParts: ConnectionKeyParts;
  /** QUIC connection generation; bumps on network change / 0-RTT reject. */
  readonly generation: number;
  state: ConnectionState = "active";
  freezeReason: FreezeReason | null = null;

  readonly sessions = new Map<number, Session>();
  /** Monotonic per-connection session ID allocator; IDs are never reused. */
  private nextSessionId = 1;
  /** QUIC stream ID -> session ID mapping (shared, connection-scoped). */
  readonly streamIdMap = new Map<number, number>();
  private nextStreamId = 0;
  /** Connection-level congestion stats (shared across origins by design). */
  readonly congestion: CongestionStats = {
    bytesInFlight: 0,
    smoothedRttMs: 0,
    congestionWindow: 0,
  };
  /** Validated ORIGIN frame advertised by the server, if any. */
  originFrame: OriginFrame | null = null;

  constructor(key: ConnectionKey, keyParts: ConnectionKeyParts, generation: number) {
    this.key = key;
    this.keyParts = keyParts;
    this.generation = generation;
  }

  get cert(): CertIdentity {
    return this.keyParts.cert;
  }

  /** New sessions may only be allocated on an active connection. */
  get canAllocateSession(): boolean {
    return this.state === "active";
  }

  /**
   * Decide whether `origin` may open a session on this connection.
   *
   * Pooling is not authorization: a session may be created only if
   *   1. the verified certificate covers the origin's host, AND
   *   2. either the origin is same-origin with an existing session on this
   *      connection, or a *validated* ORIGIN frame explicitly lists it.
   */
  authorizeOrigin(origin: string): { ok: true } | { ok: false; reason: string } {
    const canonical = canonicalizeOrigin(origin);
    if (canonical === null) return { ok: false, reason: "invalid-origin" };
    const host = new URL(canonical).hostname;

    if (!certCoversHost(this.cert, host)) {
      return { ok: false, reason: "cert-does-not-cover-origin" };
    }

    // The first session defines the connection's origin tenancy.
    if (this.sessions.size === 0) return { ok: true };

    for (const session of this.sessions.values()) {
      if (session.origin === canonical) return { ok: true };
    }

    const frame = this.originFrame;
    if (frame !== null && frame.validated) {
      const listed = frame.origins
        .map((o) => canonicalizeOrigin(o))
        .filter((o): o is string => o !== null);
      if (listed.includes(canonical)) return { ok: true };
      return { ok: false, reason: "origin-not-in-origin-frame" };
    }
    return { ok: false, reason: "no-same-origin-session-and-no-origin-frame" };
  }

  /** Allocate a fresh, never-reused session ID. Caller-supplied IDs ignored. */
  allocateSessionId(): number {
    return this.nextSessionId++;
  }

  /** Register a QUIC stream for a session; returns the new stream ID. */
  allocateStreamId(sessionId: number): number {
    const id = this.nextStreamId;
    this.nextStreamId += 4; // client-initiated bidirectional stride
    this.streamIdMap.set(id, sessionId);
    return id;
  }

  freeze(reason: FreezeReason): void {
    if (this.state === "closed") return;
    this.state = reason === "goaway" ? "draining" : "frozen";
    this.freezeReason = reason;
  }

  /** Network change: existing sessions cannot migrate; close them all. */
  closeAllSessions(): void {
    for (const session of this.sessions.values()) session.close();
  }

  close(): void {
    this.closeAllSessions();
    this.state = "closed";
  }
}
