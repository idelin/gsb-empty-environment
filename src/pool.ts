import { PooledConnection } from "./connection.ts";
import { Session, SessionHandle } from "./session.ts";
import type {
  ConnectionKey,
  ConnectionKeyParts,
  CongestionStats,
  DatagramVerdict,
  FreezeReason,
  SessionRequest,
} from "./types.ts";
import {
  canonicalizeKey,
  canonicalizeOrigin,
  canonicalizeUrl,
} from "./types.ts";

export type CreateSessionResult =
  | { ok: true; handle: SessionHandle; pooled: boolean }
  | { ok: false; reason: string };

/**
 * WebTransport session pool for the browser network stack.
 *
 * Security invariants:
 *  - Connection identity binds partition key, proxy chain, verified cert
 *    identity, ECH result, ALPN, remote endpoint and QUIC generation.
 *  - A session is bound to its requesting origin, canonical URL, the
 *    server-returned draft/version, its pool-allocated session ID and its
 *    stream/datagram quotas.
 *  - Co-tenancy on one connection requires cert coverage AND (same-origin
 *    session OR a validated ORIGIN frame). Pooling != authorization.
 *  - Only session IDs, stream-ID mapping and congestion stats are shared
 *    across origins; all other application state is origin-isolated.
 *  - GOAWAY / cert update / network change / 0-RTT reject freeze new
 *    allocations on the affected connection.
 *  - Late datagrams route by (generation, session ID); unknown or closed
 *    IDs are dropped.
 */
export class WebTransportSessionPool {
  /** Active/draining connections by canonical key. */
  private readonly byKey = new Map<ConnectionKey, PooledConnection>();
  /** Every connection ever created, indexed by QUIC generation. */
  private readonly byGeneration = new Map<number, PooledConnection>();
  private nextGeneration = 1;
  /** Datagram drop counters, for diagnostics/telemetry. */
  readonly drops = {
    unknownGeneration: 0,
    unknownSession: 0,
    closedSession: 0,
  };

  /** Look up the current allocatable connection for these key parts. */
  private allocatableConnection(parts: ConnectionKeyParts): PooledConnection | null {
    const key = canonicalizeKey(parts);
    const conn = this.byKey.get(key);
    if (conn !== undefined && conn.canAllocateSession) return conn;
    return null;
  }

  /** Get or create a connection for the given identity. */
  getOrCreateConnection(parts: ConnectionKeyParts): PooledConnection {
    const existing = this.allocatableConnection(parts);
    if (existing !== null) return existing;
    const key = canonicalizeKey(parts);
    const conn = new PooledConnection(key, parts, this.nextGeneration++);
    this.byKey.set(key, conn);
    this.byGeneration.set(conn.generation, conn);
    return conn;
  }

  /**
   * Open a WebTransport session. Reuses a pooled connection only when the
   * connection identity matches exactly AND the origin is authorized for
   * co-tenancy; otherwise creates a fresh connection (or fails).
   *
   * `serverDraft`/`serverVersion` are the values returned by the server in
   * the CONNECT response and are bound to the session.
   */
  createSession(
    parts: ConnectionKeyParts,
    request: SessionRequest,
    serverDraft: string,
    serverVersion: string,
    quotas: { maxStreams: number; maxDatagrams: number },
  ): CreateSessionResult {
    const origin = canonicalizeOrigin(request.origin);
    if (origin === null) return { ok: false, reason: "invalid-origin" };
    const url = canonicalizeUrl(request.url);
    if (url === null) return { ok: false, reason: "invalid-url" };

    const key = canonicalizeKey(parts);
    const existing = this.byKey.get(key);

    let conn: PooledConnection;
    let pooled = false;
    if (existing !== undefined) {
      if (!existing.canAllocateSession) {
        // Frozen/draining: never allocate on it; build a new generation.
        conn = this.freshConnection(parts, key);
      } else {
        const auth = existing.authorizeOrigin(origin);
        if (!auth.ok) return { ok: false, reason: auth.reason };
        conn = existing;
        pooled = true;
      }
    } else {
      // First session on a new connection; the certificate must still
      // cover the origin's host (enforced inside authorizeOrigin).
      conn = this.freshConnection(parts, key);
      const auth = conn.authorizeOrigin(origin);
      if (!auth.ok) return { ok: false, reason: auth.reason };
    }

    const session = new Session({
      id: conn.allocateSessionId(),
      origin,
      url,
      draft: serverDraft,
      version: serverVersion,
      maxStreams: quotas.maxStreams,
      maxDatagrams: quotas.maxDatagrams,
    });
    conn.sessions.set(session.id, session);
    return { ok: true, handle: new SessionHandle(session, origin), pooled };
  }

  private freshConnection(parts: ConnectionKeyParts, key: ConnectionKey): PooledConnection {
    const conn = new PooledConnection(key, parts, this.nextGeneration++);
    this.byKey.set(key, conn);
    this.byGeneration.set(conn.generation, conn);
    return conn;
  }

  /**
   * Freeze new allocations on the connection identified by `parts`.
   *  - goaway: existing sessions keep running, no new ones (draining).
   *  - cert-update / zero-rtt-rejected: frozen, existing sessions stay but
   *    the next request builds a new generation.
   *  - network-change: frozen and all sessions closed (they cannot migrate).
   */
  freeze(parts: ConnectionKeyParts, reason: FreezeReason): void {
    const conn = this.byKey.get(canonicalizeKey(parts));
    if (conn === undefined) return;
    conn.freeze(reason);
    if (reason === "network-change") conn.closeAllSessions();
  }

  /**
   * Route an incoming datagram by QUIC generation + session ID.
   * Unknown generations/sessions and closed sessions are dropped.
   */
  routeDatagram(
    generation: number,
    sessionId: number,
    payload: Uint8Array,
  ): DatagramVerdict {
    const conn = this.byGeneration.get(generation);
    if (conn === undefined) {
      this.drops.unknownGeneration++;
      return "dropped-unknown-generation";
    }
    const session = conn.sessions.get(sessionId);
    if (session === undefined) {
      this.drops.unknownSession++;
      return "dropped-unknown-session";
    }
    if (session.state !== "open") {
      this.drops.closedSession++;
      return "dropped-closed-session";
    }
    session.receiveDatagram(payload);
    return "delivered";
  }

  /** Resolve a QUIC stream ID to its owning session on a connection. */
  sessionForStream(generation: number, streamId: number): Session | null {
    const conn = this.byGeneration.get(generation);
    if (conn === undefined) return null;
    const sessionId = conn.streamIdMap.get(streamId);
    if (sessionId === undefined) return null;
    return conn.sessions.get(sessionId) ?? null;
  }

  /**
   * Congestion stats are connection-level and intentionally readable by any
   * origin co-tenant; they are the only cross-origin shared mutable state
   * besides the ID allocators.
   */
  congestionStats(generation: number): CongestionStats | null {
    return this.byGeneration.get(generation)?.congestion ?? null;
  }

  /** Close a session via its handle; late datagrams to it are dropped. */
  closeSession(handle: SessionHandle): void {
    handle.session.close();
  }

  /** Number of live connections (for tests/diagnostics). */
  get connectionCount(): number {
    return this.byKey.size;
  }
}
