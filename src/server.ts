/**
 * 零依赖的 RFC 6455 服务端集成层（基于 node:http + node:net）。
 *
 * 仅实现本包需要的服务端能力：HTTP Upgrade 握手、Sec-WebSocket-Protocol
 * 协商、数据帧收发、ping/pong/close 控制帧，以及按子协议策略的 1008 门控。
 * 生产项目也可以复用 negotiateSubprotocol / enforceMessage 接入 ws 等成熟库，
 * 本模块同时是集成测试的参考实现。
 */

import { EventEmitter } from "node:events";
import { createHash } from "node:crypto";
import type { IncomingMessage, Server as HttpServer } from "node:http";
import { createServer } from "node:http";
import type { Duplex } from "node:stream";
import type { Socket } from "node:net";
import { negotiateSubprotocol, type BearerToken } from "./negotiate.ts";
import { decideFrame, type FrameKind, type MessagePolicy } from "./gate.ts";

const WS_GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11";
const MAX_PAYLOAD_BYTES = 1 << 20;
const MAX_CONTROL_PAYLOAD = 125;
const CLOSE_REASON_LIMIT = 123;

export const READY_STATE = {
  CONNECTING: 0,
  OPEN: 1,
  CLOSING: 2,
  CLOSED: 3,
} as const;

export interface NegotiatingServerOptions {
  /** 服务端子协议优先级（高 -> 低）。 */
  serverProtocols: string[];
  /** 每个子协议允许的消息类型；未配置的子协议拒绝一切数据帧。 */
  policies: Record<string, MessagePolicy>;
  /** 应用层消息类型字段名，默认 "type"。 */
  appTypeField?: string;
}

export class NegotiatedConnection extends EventEmitter {
  readonly subprotocol: string | null;
  readonly bearerToken: BearerToken | null;
  readonly policy: MessagePolicy | null;
  readonly remoteAddress: string | undefined;
  readyState: number = READY_STATE.CONNECTING;

  #socket: Duplex;
  #appTypeField: string;
  #buffer: Buffer = Buffer.alloc(0);
  #fragmentOpcode: FrameKind | null = null;
  #fragments: Buffer[] = [];
  #closeReceived = false;
  #closeSent = false;

  constructor(
    socket: Duplex,
    subprotocol: string | null,
    bearerToken: BearerToken | null,
    policy: MessagePolicy | null,
    appTypeField: string,
  ) {
    super();
    this.#socket = socket;
    this.subprotocol = subprotocol;
    this.bearerToken = bearerToken;
    this.policy = policy;
    this.remoteAddress = isNetworkSocket(socket) ? socket.remoteAddress : undefined;
    this.#appTypeField = appTypeField;
    this.readyState = READY_STATE.OPEN;

    socket.on("data", (chunk: Buffer) => this.#onData(chunk));
    socket.on("error", (error: Error) => this.emit("error", error, this));
    socket.on("close", () => this.#onSocketClose());
  }

  sendText(payload: string): void {
    if (this.readyState !== READY_STATE.OPEN) return;
    this.#writeFrame(0x1, Buffer.from(payload, "utf8"));
  }

  sendBinary(payload: Uint8Array): void {
    if (this.readyState !== READY_STATE.OPEN) return;
    this.#writeFrame(0x2, Buffer.from(payload));
  }

  ping(payload: Uint8Array = Buffer.alloc(0)): void {
    if (this.readyState !== READY_STATE.OPEN) return;
    this.#writeFrame(0x9, Buffer.from(payload));
  }

  /** 发送 close 帧；收到对端 close 后也会调用，完成关闭握手。 */
  close(code = 1000, reason = ""): void {
    if (this.#closeSent) return;
    this.#closeSent = true;
    this.readyState = this.#closeReceived ? READY_STATE.CLOSED : READY_STATE.CLOSING;

    const reasonBytes = Buffer.from(reason, "utf8").subarray(0, CLOSE_REASON_LIMIT);
    const payload = Buffer.alloc(2 + reasonBytes.length);
    payload.writeUInt16BE(code, 0);
    reasonBytes.copy(payload, 2);
    this.#writeFrame(0x8, payload);

    if (this.#closeReceived) {
      this.#socket.end();
    }
  }

  terminate(): void {
    this.readyState = READY_STATE.CLOSED;
    this.#socket.destroy();
  }

  /** 灌入握手响应前已到达的帧字节（Node upgrade 事件的 head 参数）。 */
  feed(chunk: Buffer): void {
    if (chunk.length > 0) this.#onData(chunk);
  }

  #onData(chunk: Buffer): void {
    this.#buffer = this.#buffer.length === 0 ? chunk : Buffer.concat([this.#buffer, chunk]);
    this.#drainFrames();
  }

  #drainFrames(): void {
    while (this.#buffer.length >= 2) {
      const first = this.#buffer[0]!;
      const second = this.#buffer[1]!;
      const fin = (first & 0x80) === 0x80;
      const opcode = first & 0x0f;
      const masked = (second & 0x80) === 0x80;

      if (!masked) {
        this.#failProtocol(1002, "client frames must be masked");
        return;
      }

      let payloadLength = second & 0x7f;
      let offset = 2;
      if (payloadLength === 126) {
        if (this.#buffer.length < offset + 2) return;
        payloadLength = this.#buffer.readUInt16BE(offset);
        offset += 2;
      } else if (payloadLength === 127) {
        if (this.#buffer.length < offset + 8) return;
        const high = this.#buffer.readUInt32BE(offset);
        const low = this.#buffer.readUInt32BE(offset + 4);
        payloadLength = high * 2 ** 32 + low;
        offset += 8;
      }

      if (payloadLength > MAX_PAYLOAD_BYTES) {
        this.#failProtocol(1009, "payload exceeds 1 MiB limit");
        return;
      }
      if (this.#buffer.length < offset + 4 + payloadLength) return;

      const maskingKey = this.#buffer.subarray(offset, offset + 4);
      offset += 4;
      const payload = this.#buffer.subarray(offset, offset + payloadLength);
      for (let index = 0; index < payload.length; index += 1) {
        payload[index] = payload[index]! ^ maskingKey[index % 4]!;
      }
      this.#buffer = this.#buffer.subarray(offset + payloadLength);

      this.#handleFrame(fin, opcode, Buffer.from(payload));
      if (this.readyState === READY_STATE.CLOSED) return;
    }
  }

  #handleFrame(fin: boolean, opcode: number, payload: Buffer): void {
    if ((opcode >= 0x3 && opcode <= 0x7) || (opcode >= 0xb && opcode <= 0xf)) {
      this.#failProtocol(1002, "reserved opcode is not supported");
      return;
    }

    if (opcode === 0x8) {
      this.#handleClose(payload);
      return;
    }
    if (opcode === 0x9) {
      if (!fin || payload.length > MAX_CONTROL_PAYLOAD) {
        this.#failProtocol(1002, "malformed ping frame");
        return;
      }
      this.#writeFrame(0xa, payload);
      return;
    }
    if (opcode === 0xa) {
      if (!fin || payload.length > MAX_CONTROL_PAYLOAD) {
        this.#failProtocol(1002, "malformed pong frame");
      }
      return;
    }

    if (opcode === 0x0) {
      if (this.#fragmentOpcode === null) {
        this.#failProtocol(1002, "continuation frame without a start frame");
        return;
      }
      this.#fragments.push(payload);
    } else if (opcode === 0x1 || opcode === 0x2) {
      if (this.#fragmentOpcode !== null) {
        this.#failProtocol(1002, "nested message start frame");
        return;
      }
      this.#fragmentOpcode = opcode === 0x1 ? "text" : "binary";
      this.#fragments = [payload];
    }

    if (fin) {
      const kind = this.#fragmentOpcode;
      const message = Buffer.concat(this.#fragments);
      this.#fragmentOpcode = null;
      this.#fragments = [];
      if (kind === null) {
        this.#failProtocol(1002, "finished frame without a message start");
        return;
      }
      this.#dispatchMessage(kind, message);
    }
  }

  #dispatchMessage(kind: FrameKind, payload: Buffer): void {
    if (this.policy === null) {
      this.close(1008, "no message policy for the negotiated subprotocol");
      return;
    }

    if (kind === "text") {
      const decoder = new TextDecoder("utf-8", { fatal: true });
      let text: string;
      try {
        text = decoder.decode(payload);
      } catch {
        this.#failProtocol(1007, "invalid UTF-8 text frame");
        return;
      }
      const decision = decideFrame("text", text, this.policy, this.#appTypeField);
      if (!decision.allowed) {
        this.close(decision.closeCode, decision.reason);
        return;
      }
      this.emit("text", text, this);
      return;
    }

    const decision = decideFrame("binary", payload, this.policy, this.#appTypeField);
    if (!decision.allowed) {
      this.close(decision.closeCode, decision.reason);
      return;
    }
    this.emit("binary", payload, this);
  }

  #handleClose(payload: Buffer): void {
    if (payload.length === 1) {
      this.#failProtocol(1002, "malformed close frame");
      return;
    }
    let code = 1005;
    let reason = "";
    if (payload.length >= 2) {
      code = payload.readUInt16BE(0);
      if (!isValidCloseCode(code)) {
        this.#failProtocol(1002, `invalid close code ${code}`);
        return;
      }
      try {
        reason = new TextDecoder("utf-8", { fatal: true }).decode(payload.subarray(2));
      } catch {
        this.#failProtocol(1007, "close reason is not valid UTF-8");
        return;
      }
    }

    this.#closeReceived = true;
    if (!this.#closeSent) {
      this.close(code, reason);
    } else {
      this.readyState = READY_STATE.CLOSED;
      this.#socket.end();
    }
    this.emit("close", code, reason, this);
  }

  #failProtocol(code: number, reason: string): void {
    if (this.readyState === READY_STATE.CLOSED) return;
    this.readyState = READY_STATE.CLOSED;
    this.#fragmentOpcode = null;
    this.#fragments = [];
    this.close(code, reason);
  }

  #writeFrame(opcode: number, payload: Buffer): void {
    const header: number[] = [0x80 | opcode];
    if (payload.length < 126) {
      header.push(payload.length);
    } else if (payload.length <= 0xffff) {
      header.push(126);
    } else {
      header.push(127);
    }
    const parts: Buffer[] = [Buffer.from(header)];

    if (payload.length >= 126 && payload.length <= 0xffff) {
      const extended = Buffer.alloc(2);
      extended.writeUInt16BE(payload.length, 0);
      parts.push(extended);
    } else if (payload.length > 0xffff) {
      const extended = Buffer.alloc(8);
      extended.writeUInt32BE(0, 0);
      extended.writeUInt32BE(payload.length, 4);
      parts.push(extended);
    }
    parts.push(payload);
    this.#socket.write(Buffer.concat(parts));
  }

  #onSocketClose(): void {
    this.readyState = READY_STATE.CLOSED;
  }
}

function isValidCloseCode(code: number): boolean {
  if (code >= 3000 && code <= 3999) return true;
  if (code >= 4000 && code <= 4999) return true;
  return [1000, 1001, 1002, 1003, 1007, 1008, 1009, 1010, 1011, 1012, 1013, 1014, 1015].includes(code);
}

function acceptKey(key: string): string {
  return createHash("sha1").update(key + WS_GUID).digest("base64");
}

function rejectUpgrade(socket: Duplex, statusCode: number, message: string): void {
  const body = Buffer.from(`${message}\n`, "utf8");
  socket.write(
    `HTTP/1.1 ${statusCode} ${message}\r\n` +
      "Connection: close\r\n" +
      `Content-Length: ${body.length}\r\n` +
      "Content-Type: text/plain; charset=utf-8\r\n\r\n",
  );
  socket.write(body);
  socket.destroy();
}

function isNetworkSocket(socket: Duplex): socket is Socket {
  return "remoteAddress" in socket;
}

export class NegotiatingWebSocketServer extends EventEmitter {
  #httpServer: HttpServer;
  #options: NegotiatingServerOptions;

  constructor(options: NegotiatingServerOptions) {
    super();
    this.#options = options;
    this.#httpServer = createServer((request, response) => {
      response.writeHead(426, { Connection: "upgrade", Upgrade: "websocket" });
      response.end("Upgrade Required: use the WebSocket protocol\n");
    });
    this.#httpServer.on("upgrade", (request, socket, head) => this.#handleUpgrade(request, socket, head));
  }

  get port(): number | undefined {
    const address = this.#httpServer.address();
    return typeof address === "object" && address !== null ? address.port : undefined;
  }

  listen(port = 0, host?: string): Promise<number> {
    return new Promise((resolve, reject) => {
      const server = this.#httpServer.listen(port, host);
      server.once("listening", () => resolve(this.port ?? 0));
      server.once("error", reject);
    });
  }

  close(): Promise<void> {
    return new Promise((resolve, reject) => {
      this.#httpServer.close((error) => (error ? reject(error) : resolve()));
    });
  }

  /**
   * 把一条已连接的 Duplex（内存管道或自定义传输）交给内部 HTTP 服务，
   * 完整走 upgrade 事件与协商逻辑，便于在禁止监听端口的环境里测试。
   */
  attachTransport(transport: Duplex): void {
    this.#httpServer.emit("connection", transport as unknown as Socket);
  }

  #handleUpgrade(request: IncomingMessage, socket: Duplex, head: Buffer): void {
    if (request.headers.upgrade?.toLowerCase() !== "websocket") {
      rejectUpgrade(socket, 400, "Bad Request: expected Upgrade: websocket");
      return;
    }
    if (request.headers.connection === undefined || !/upgrade/i.test(request.headers.connection)) {
      rejectUpgrade(socket, 400, "Bad Request: expected Connection: upgrade");
      return;
    }
    if (request.headers["sec-websocket-version"] !== "13") {
      rejectUpgrade(socket, 426, "Upgrade Required: only Sec-WebSocket-Version 13 is supported");
      return;
    }
    const key = request.headers["sec-websocket-key"];
    if (typeof key !== "string" || !/^[A-Za-z0-9+/]{22}==$/.test(key)) {
      rejectUpgrade(socket, 400, "Bad Request: invalid Sec-WebSocket-Key");
      return;
    }

    const result = negotiateSubprotocol(request.headers["sec-websocket-protocol"], {
      serverProtocols: this.#options.serverProtocols,
    });

    if (!result.ok) {
      this.emit("reject", { reason: result.reason, audit: result.audit, request });
      rejectUpgrade(socket, 400, `Bad Request: ${result.reason}`);
      return;
    }

    const lines = [
      "HTTP/1.1 101 Switching Protocols",
      "Upgrade: websocket",
      "Connection: Upgrade",
      `Sec-WebSocket-Accept: ${acceptKey(key)}`,
    ];
    const responseHeaders = result.toResponseHeaders();
    for (const [name, value] of Object.entries(responseHeaders)) {
      lines.push(`${name}: ${value}`);
    }
    socket.write(`${lines.join("\r\n")}\r\n\r\n`);

    const policy = result.subprotocol === null ? null : (this.#options.policies[result.subprotocol] ?? null);
    const connection = new NegotiatedConnection(
      socket,
      result.subprotocol,
      result.bearerToken,
      policy,
      this.#options.appTypeField ?? "type",
    );
    connection.feed(head);
    this.emit("connection", connection, request);
  }
}
