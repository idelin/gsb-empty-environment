import { randomBytes } from "node:crypto";
import type { Duplex } from "node:stream";
import { encodeFrame, FrameParser, OPCODES, sendClose, sendText } from "../src/ws.ts";

export interface HandshakeResponse {
  readonly status: number;
  readonly headers: Map<string, string>;
  readonly body: string;
}

export interface WsClientOptions {
  readonly origin?: string;
  readonly cookie?: string;
  readonly protocols?: readonly string[];
}

/** Minimal WebSocket client that runs over any duplex stream (no TCP needed). */
export class WsClient {
  private socket: Duplex | null = null;
  private readonly parser = new FrameParser();
  private readonly waiters: Array<{ resolve: (msg: string) => void; reject: (err: Error) => void }> = [];
  private readonly queue: string[] = [];
  private closeInfo: { code: number; reason: string } | null = null;
  private closeWaiters: Array<(info: { code: number; reason: string }) => void> = [];

  static handshake(
    socket: Duplex,
    options: WsClientOptions,
  ): Promise<{ response: HandshakeResponse; client: WsClient | null }> {
    const key = randomBytes(16).toString("base64");
    const headers: Record<string, string> = {
      Host: "chat.example.com",
      Upgrade: "websocket",
      Connection: "Upgrade",
      "Sec-WebSocket-Key": key,
      "Sec-WebSocket-Version": "13",
    };
    if (options.origin !== undefined) headers["Origin"] = options.origin;
    if (options.cookie !== undefined) headers["Cookie"] = options.cookie;
    if (options.protocols !== undefined) {
      headers["Sec-WebSocket-Protocol"] = options.protocols.join(", ");
    }
    const raw = Object.entries(headers)
      .map(([name, value]) => `${name}: ${value}`)
      .join("\r\n");
    socket.write(`GET /chat HTTP/1.1\r\n${raw}\r\n\r\n`);
    return new Promise((resolve, reject) => {
      let buffer = Buffer.alloc(0);
      let settled = false;
      const onData = (chunk: Buffer): void => {
        buffer = Buffer.concat([buffer, chunk]);
        const end = buffer.indexOf("\r\n\r\n");
        if (end === -1) return;
        socket.off("data", onData);
        const head = buffer.subarray(0, end).toString("utf8");
        const rest = buffer.subarray(end + 4);
        const [statusLine, ...headerLines] = head.split("\r\n");
        const status = Number(statusLine.split(" ")[1]);
        const parsed = new Map<string, string>();
        for (const line of headerLines) {
          const colon = line.indexOf(":");
          if (colon > 0) {
            parsed.set(line.slice(0, colon).trim().toLowerCase(), line.slice(colon + 1).trim());
          }
        }
        if (status !== 101) {
          settled = true;
          let body = rest.toString("utf8");
          socket.on("data", (c: Buffer) => (body += c.toString("utf8")));
          socket.on("close", () =>
            resolve({ response: { status, headers: parsed, body }, client: null }),
          );
          return;
        }
        settled = true;
        const client = new WsClient();
        client.attach(socket);
        if (rest.length > 0) client.feed(rest);
        resolve({ response: { status, headers: parsed, body: "" }, client });
      };
      socket.on("data", onData);
      socket.on("close", () => {
        if (!settled) reject(new Error("socket closed before handshake completed"));
      });
      socket.on("error", reject);
    });
  }

  private attach(socket: Duplex): void {
    this.socket = socket;
    socket.on("data", (chunk: Buffer) => this.feed(chunk));
    socket.on("close", () => {
      if (!this.closeInfo) this.closeInfo = { code: 1006, reason: "abnormal" };
      for (const waiter of this.closeWaiters.splice(0)) waiter(this.closeInfo);
      for (const waiter of this.waiters.splice(0)) waiter.reject(new Error("socket closed"));
    });
    socket.on("error", () => undefined);
  }

  private feed(chunk: Buffer): void {
    for (const frame of this.parser.push(chunk)) {
      if (frame.opcode === OPCODES.text) {
        const message = frame.payload.toString("utf8");
        const waiter = this.waiters.shift();
        if (waiter) waiter.resolve(message);
        else this.queue.push(message);
      } else if (frame.opcode === OPCODES.close) {
        const code = frame.payload.length >= 2 ? frame.payload.readUInt16BE(0) : 1005;
        const reason = frame.payload.subarray(2).toString("utf8");
        this.closeInfo = { code, reason };
        for (const waiter of this.closeWaiters.splice(0)) waiter(this.closeInfo);
        this.socket?.end();
      } else if (frame.opcode === OPCODES.ping) {
        this.socket?.write(encodeFrame(OPCODES.pong, frame.payload, true));
      }
    }
  }

  send(text: string): void {
    if (!this.socket) throw new Error("not connected");
    sendText(this.socket, text, true);
  }

  nextMessage(timeoutMs = 2000): Promise<string> {
    const queued = this.queue.shift();
    if (queued !== undefined) return Promise.resolve(queued);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("timed out waiting for message")), timeoutMs);
      this.waiters.push({
        resolve: (msg) => {
          clearTimeout(timer);
          resolve(msg);
        },
        reject: (err) => {
          clearTimeout(timer);
          reject(err);
        },
      });
    });
  }

  waitForClose(timeoutMs = 2000): Promise<{ code: number; reason: string }> {
    if (this.closeInfo) return Promise.resolve(this.closeInfo);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("timed out waiting for close")), timeoutMs);
      this.closeWaiters.push((info) => {
        clearTimeout(timer);
        resolve(info);
      });
    });
  }

  close(): void {
    if (this.socket && !this.closeInfo) sendClose(this.socket, 1000, "done", true);
    this.socket?.end();
  }
}
