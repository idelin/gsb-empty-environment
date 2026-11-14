import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import type { Duplex } from "node:stream";
import {
  acceptKey,
  authenticateHandshake,
  createSessionStore,
  parseCookies,
  sessionCookie,
  type AuthContext,
  type Role,
  type SessionStore,
} from "./auth.ts";
import { encodeFrame, FrameParser, OPCODES, sendClose, sendText } from "./ws.ts";

export interface MessageServiceOptions {
  readonly allowedOrigins: readonly string[];
  readonly supportedProtocols?: readonly string[];
  readonly sessions?: SessionStore;
}

interface Connection {
  readonly socket: Duplex;
  readonly context: AuthContext;
}

export interface MessageService {
  readonly server: Server;
  readonly sessions: SessionStore;
  listen(port?: number): Promise<number>;
  close(): Promise<void>;
}

const json = (res: ServerResponse, status: number, body: unknown): void => {
  const data = JSON.stringify(body);
  res.writeHead(status, { "content-type": "application/json" });
  res.end(data);
};

async function readBody(req: IncomingMessage): Promise<string> {
  const chunks: Buffer[] = [];
  for await (const chunk of req) chunks.push(chunk as Buffer);
  return Buffer.concat(chunks).toString("utf8");
}

/**
 * Message service with a shared authorization path: the WebSocket handshake
 * and every HTTP endpoint resolve the same session cookie through the same
 * session store, and connections are bound to session + origin + role +
 * generation so permission changes close stale sockets.
 */
export function createMessageService(options: MessageServiceOptions): MessageService {
  const sessions = options.sessions ?? createSessionStore();
  const supportedProtocols = options.supportedProtocols ?? ["chat.v1"];
  const connections = new Set<Connection>();

  /** HTTP-side authorization: identical session resolution to the handshake. */
  function authorizeHttp(req: IncomingMessage): AuthContext | null {
    const sessionId = parseCookies(req.headers.cookie).get("sid");
    if (!sessionId) return null;
    const session = sessions.get(sessionId);
    if (!session) return null;
    const originHeader = req.headers.origin;
    const origin = typeof originHeader === "string" ? originHeader : "";
    return {
      sessionId: session.id,
      userId: session.userId,
      role: session.role,
      generation: session.generation,
      origin,
    };
  }

  /** Close every connection whose session generation is now stale. */
  function closeStaleConnections(): void {
    for (const conn of [...connections]) {
      const session = sessions.get(conn.context.sessionId);
      if (!session || session.generation !== conn.context.generation) {
        sendClose(conn.socket, 4001, "session invalidated");
        conn.socket.destroy();
        connections.delete(conn);
      }
    }
  }

  const server = createServer((req, res) => {
    void handleHttp(req, res).catch(() => json(res, 500, { error: "internal" }));
  });

  async function handleHttp(req: IncomingMessage, res: ServerResponse): Promise<void> {
    const url = new URL(req.url ?? "/", "http://localhost");

    if (req.method === "POST" && url.pathname === "/login") {
      const body = JSON.parse(await readBody(req) || "{}") as { userId?: string; role?: Role };
      if (!body.userId) return json(res, 400, { error: "userId required" });
      const session = sessions.create(body.userId, body.role ?? "user");
      res.writeHead(200, {
        "content-type": "application/json",
        "set-cookie": sessionCookie(session.id),
      });
      res.end(JSON.stringify({ sessionId: session.id, userId: session.userId }));
      return;
    }

    const auth = authorizeHttp(req);
    if (!auth) return json(res, 401, { error: "unauthorized" });

    if (req.method === "GET" && url.pathname === "/whoami") {
      return json(res, 200, { userId: auth.userId, role: auth.role, generation: auth.generation });
    }

    if (req.method === "POST" && url.pathname === "/admin/set-role") {
      if (auth.role !== "admin") return json(res, 403, { error: "admin role required" });
      const body = JSON.parse(await readBody(req) || "{}") as { userId?: string; role?: Role };
      if (!body.userId || !body.role) return json(res, 400, { error: "userId and role required" });
      sessions.setRole(body.userId, body.role);
      closeStaleConnections();
      return json(res, 200, { ok: true });
    }

    if (req.method === "POST" && url.pathname === "/admin/revoke") {
      if (auth.role !== "admin") return json(res, 403, { error: "admin role required" });
      const body = JSON.parse(await readBody(req) || "{}") as { userId?: string };
      if (!body.userId) return json(res, 400, { error: "userId required" });
      sessions.revoke(body.userId);
      closeStaleConnections();
      return json(res, 200, { ok: true });
    }

    json(res, 404, { error: "not found" });
  }

  server.on("upgrade", (req: IncomingMessage, socket: Duplex, head: Buffer) => {
    const result = authenticateHandshake(req, {
      allowedOrigins: options.allowedOrigins,
      supportedProtocols,
      sessions,
    });
    if (!result.ok) {
      socket.write(
        `HTTP/1.1 ${result.status} Rejected\r\ncontent-type: text/plain\r\nconnection: close\r\n\r\n${result.reason}`,
      );
      socket.destroy();
      return;
    }

    const key = req.headers["sec-websocket-key"] as string;
    socket.write(
      "HTTP/1.1 101 Switching Protocols\r\n" +
        "Upgrade: websocket\r\n" +
        "Connection: Upgrade\r\n" +
        `Sec-WebSocket-Accept: ${acceptKey(key)}\r\n` +
        `Sec-WebSocket-Protocol: ${result.protocol}\r\n` +
        "\r\n",
    );

    const conn: Connection = { socket, context: result.context };
    connections.add(conn);
    const parser = new FrameParser();
    if (head.length > 0) handleFrames(conn, parser.push(head));
    socket.on("data", (chunk: Buffer) => handleFrames(conn, parser.push(chunk)));
    socket.on("close", () => connections.delete(conn));
    socket.on("error", () => connections.delete(conn));
  });

  function handleFrames(conn: Connection, frames: ReturnType<FrameParser["push"]>): void {
    for (const frame of frames) {
      if (frame.opcode === OPCODES.close) {
        sendClose(conn.socket, 1000, "bye");
        conn.socket.end();
        connections.delete(conn);
      } else if (frame.opcode === OPCODES.ping) {
        conn.socket.write(encodeFrame(OPCODES.pong, frame.payload, false));
      } else if (frame.opcode === OPCODES.text) {
        sendText(
          conn.socket,
          JSON.stringify({
            from: conn.context.userId,
            role: conn.context.role,
            generation: conn.context.generation,
            echo: frame.payload.toString("utf8"),
          }),
        );
      }
    }
  }

  return {
    server,
    sessions,
    listen(port = 0) {
      return new Promise((resolve) => {
        server.listen(port, "127.0.0.1", () => {
          const address = server.address();
          resolve(typeof address === "object" && address ? address.port : 0);
        });
      });
    },
    close() {
      for (const conn of connections) conn.socket.destroy();
      connections.clear();
      return new Promise((resolve, reject) => {
        if (!server.listening) return resolve();
        server.close((err) => (err ? reject(err) : resolve()));
      });
    },
  };
}
