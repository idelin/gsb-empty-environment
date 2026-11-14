import { createHash, randomBytes, timingSafeEqual } from "node:crypto";
import type { IncomingMessage } from "node:http";

export type Role = "user" | "admin";

export interface Session {
  readonly id: string;
  readonly userId: string;
  readonly role: Role;
  readonly generation: number;
  readonly createdAt: number;
}

export interface SessionStore {
  create(userId: string, role: Role): Session;
  get(sessionId: string): Session | undefined;
  setRole(userId: string, role: Role): void;
  revoke(userId: string): void;
}

export function createSessionStore(): SessionStore {
  const sessions = new Map<string, Session>();
  return {
    create(userId, role) {
      const session: Session = {
        id: randomBytes(16).toString("hex"),
        userId,
        role,
        generation: 1,
        createdAt: Date.now(),
      };
      sessions.set(session.id, session);
      return session;
    },
    get(sessionId) {
      return sessions.get(sessionId);
    },
    setRole(userId, role) {
      for (const [id, session] of sessions) {
        if (session.userId === userId) {
          sessions.set(id, { ...session, role, generation: session.generation + 1 });
        }
      }
    },
    revoke(userId) {
      for (const [id, session] of sessions) {
        if (session.userId === userId) {
          sessions.delete(id);
        }
      }
    },
  };
}

export interface AuthContext {
  readonly sessionId: string;
  readonly userId: string;
  readonly role: Role;
  readonly generation: number;
  readonly origin: string;
}

export type HandshakeResult =
  | { ok: true; context: AuthContext; protocol: string }
  | { ok: false; status: number; reason: string };

export interface HandshakeAuthOptions {
  /** Exact Origin header values allowed to upgrade. */
  readonly allowedOrigins: readonly string[];
  /** WebSocket subprotocols the service supports. */
  readonly supportedProtocols: readonly string[];
  readonly sessions: SessionStore;
  /** When true, requests without an Origin header are rejected. Default true. */
  readonly requireOrigin?: boolean;
}

const SAFE_EQUAL = (a: string, b: string): boolean => {
  const ba = Buffer.from(a);
  const bb = Buffer.from(b);
  return ba.length === bb.length && timingSafeEqual(ba, bb);
};

export function parseCookies(header: string | undefined): Map<string, string> {
  const cookies = new Map<string, string>();
  if (!header) return cookies;
  for (const part of header.split(";")) {
    const eq = part.indexOf("=");
    if (eq <= 0) continue;
    const name = part.slice(0, eq).trim();
    const value = part.slice(eq + 1).trim();
    if (name && !cookies.has(name)) cookies.set(name, value);
  }
  return cookies;
}

/**
 * Verify an incoming WebSocket upgrade request before it is accepted.
 * Checks Origin allowlist, SameSite session cookie, and subprotocol offer.
 */
export function authenticateHandshake(
  req: IncomingMessage,
  options: HandshakeAuthOptions,
): HandshakeResult {
  const requireOrigin = options.requireOrigin ?? true;

  if ((req.headers.upgrade ?? "").toLowerCase() !== "websocket") {
    return { ok: false, status: 426, reason: "expected a websocket upgrade" };
  }
  const key = req.headers["sec-websocket-key"];
  if (typeof key !== "string" || key.length === 0) {
    return { ok: false, status: 400, reason: "missing Sec-WebSocket-Key" };
  }

  const originHeader = req.headers.origin;
  const origin = typeof originHeader === "string" ? originHeader : "";
  if (!origin) {
    if (requireOrigin) {
      return { ok: false, status: 403, reason: "missing Origin header" };
    }
  } else if (!options.allowedOrigins.some((allowed) => SAFE_EQUAL(allowed, origin))) {
    return { ok: false, status: 403, reason: `origin not allowed: ${origin}` };
  }

  const cookies = parseCookies(req.headers.cookie);
  const sessionId = cookies.get("sid");
  if (!sessionId) {
    return { ok: false, status: 401, reason: "missing session cookie" };
  }
  const session = options.sessions.get(sessionId);
  if (!session) {
    return { ok: false, status: 401, reason: "unknown or expired session" };
  }

  const offeredHeader = req.headers["sec-websocket-protocol"];
  const offered =
    typeof offeredHeader === "string"
      ? offeredHeader.split(",").map((p) => p.trim()).filter(Boolean)
      : [];
  const protocol = offered.find((p) => options.supportedProtocols.includes(p));
  if (!protocol) {
    return { ok: false, status: 400, reason: "no supported subprotocol offered" };
  }

  return {
    ok: true,
    protocol,
    context: {
      sessionId: session.id,
      userId: session.userId,
      role: session.role,
      generation: session.generation,
      origin,
    },
  };
}

const WS_GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11";

export function acceptKey(secWebSocketKey: string): string {
  return createHash("sha1").update(secWebSocketKey + WS_GUID).digest("base64");
}

/** Serialize a Set-Cookie header for a hardened session cookie. */
export function sessionCookie(sessionId: string): string {
  return `sid=${sessionId}; HttpOnly; Secure; SameSite=Strict; Path=/`;
}
