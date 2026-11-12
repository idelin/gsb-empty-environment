import { Duplex } from "node:stream";
import { createHash, randomBytes } from "node:crypto";

const WS_GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11";

export interface CloseEvent {
  code: number;
  reason: string;
}

/**
 * 仅用于测试的最小 RFC 6455 客户端：通过内存 Duplex 与服务端握手，
 * 支持发送文本/二进制帧、读取 close 帧与服务端文本回显。
 */
export class MemoryWebSocketClient {
  readonly #stream: Duplex;
  #buffer: Buffer = Buffer.alloc(0);
  #handshakeResolve: (() => void) | null = null;
  #handshakeReject: ((error: Error) => void) | null = null;
  #closeResolve: ((event: CloseEvent) => void) | null = null;
  #textResolve: ((text: string) => void) | null = null;
  #handshakeDone = false;

  statusLine = "";
  headers = new Map<string, string>();
  closed: CloseEvent | null = null;

  private constructor(stream: Duplex) {
    this.#stream = stream;
    stream.on("data", (chunk: Buffer) => this.#onData(chunk));
    stream.on("error", (error: Error) => this.#handshakeReject?.(error));
    stream.on("end", () => {
      if (this.closed === null) this.closed = { code: 1006, reason: "" };
      this.#closeResolve?.(this.closed!);
    });
  }

  /**
   * 在给定的 HTTP 服务上发起内存内升级请求。
   * attach 回调把服务端那侧的 Duplex 交给 http.Server 的 connection 事件。
   */
  static async handshake(
    attach: (serverSide: Duplex) => void,
    protocols: string[],
  ): Promise<MemoryWebSocketClient> {
    const { duplexPair } = await import("./memory-transport.ts");
    const [clientSide, serverSide] = duplexPair();
    const client = new MemoryWebSocketClient(clientSide);

    const key = randomBytes(16).toString("base64");
    const headers = [
      "GET / HTTP/1.1",
      "Host: memory-test",
      "Upgrade: websocket",
      "Connection: Upgrade",
      `Sec-WebSocket-Key: ${key}`,
      "Sec-WebSocket-Version: 13",
    ];
    if (protocols.length > 0) {
      headers.push(`Sec-WebSocket-Protocol: ${protocols.join(", ")}`);
    }

    const opened = new Promise<void>((resolve, reject) => {
      client.#handshakeResolve = resolve;
      client.#handshakeReject = reject;
    });

    attach(serverSide);
    clientSide.write(`${headers.join("\r\n")}\r\n\r\n`);
    await opened;

    const expected = createHash("sha1").update(key + WS_GUID).digest("base64");
    if (client.headers.get("sec-websocket-accept") !== expected) {
      throw new Error(`invalid Sec-WebSocket-Accept in: ${client.statusLine}`);
    }
    return client;
  }

  /** 期望握手失败（HTTP 非 101）。 */
  static async rejectedHandshake(
    attach: (serverSide: Duplex) => void,
    protocols: string[],
  ): Promise<MemoryWebSocketClient> {
    const { duplexPair } = await import("./memory-transport.ts");
    const [clientSide, serverSide] = duplexPair();
    const client = new MemoryWebSocketClient(clientSide);

    const key = randomBytes(16).toString("base64");
    const done = new Promise<void>((resolve, reject) => {
      client.#handshakeResolve = resolve;
      client.#handshakeReject = reject;
    });
    attach(serverSide);
    clientSide.write(
      `GET / HTTP/1.1\r\nHost: memory-test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n` +
        `Sec-WebSocket-Key: ${key}\r\nSec-WebSocket-Version: 13\r\n` +
        (protocols.length > 0 ? `Sec-WebSocket-Protocol: ${protocols.join(", ")}\r\n` : "") +
        `\r\n`,
    );
    await done;
    return client;
  }

  get protocol(): string {
    return this.headers.get("sec-websocket-protocol") ?? "";
  }

  sendText(payload: string): void {
    this.#sendFrame(0x1, Buffer.from(payload, "utf8"));
  }

  sendBinary(payload: Uint8Array): void {
    this.#sendFrame(0x2, Buffer.from(payload));
  }

  waitForClose(): Promise<CloseEvent> {
    if (this.closed) return Promise.resolve(this.closed);
    return new Promise((resolve) => {
      this.#closeResolve = resolve;
    });
  }

  waitForText(): Promise<string> {
    return new Promise((resolve) => {
      this.#textResolve = resolve;
    });
  }

  destroy(): void {
    this.#stream.destroy();
  }

  #sendFrame(opcode: number, payload: Buffer): void {
    const mask = randomBytes(4);
    const masked = Buffer.from(payload);
    for (let index = 0; index < masked.length; index += 1) {
      masked[index] = masked[index] ^ mask[index % 4]!;
    }

    const lengthBytes: Buffer[] = [];
    let lengthByte = 0x80;
    if (payload.length < 126) {
      lengthByte |= payload.length;
    } else if (payload.length <= 0xffff) {
      lengthByte |= 126;
      const extended = Buffer.alloc(2);
      extended.writeUInt16BE(payload.length, 0);
      lengthBytes.push(extended);
    } else {
      lengthByte |= 127;
      const extended = Buffer.alloc(8);
      extended.writeUInt32BE(0, 0);
      extended.writeUInt32BE(payload.length, 4);
      lengthBytes.push(extended);
    }

    this.#stream.write(Buffer.concat([Buffer.from([0x80 | opcode, lengthByte]), ...lengthBytes, mask, masked]));
  }

  #onData(chunk: Buffer): void {
    this.#buffer = Buffer.concat([this.#buffer, chunk]);
    if (!this.#handshakeDone) {
      const boundary = this.#buffer.indexOf("\r\n\r\n");
      if (boundary === -1) return;
      const head = this.#buffer.subarray(0, boundary).toString("utf8");
      this.#buffer = this.#buffer.subarray(boundary + 4);
      const [statusLine, ...lines] = head.split("\r\n");
      this.statusLine = statusLine ?? "";
      for (const line of lines) {
        const separator = line.indexOf(":");
        if (separator > 0) {
          this.headers.set(line.slice(0, separator).trim().toLowerCase(), line.slice(separator + 1).trim());
        }
      }
      this.#handshakeDone = true;
      this.#handshakeResolve?.();
      this.#handshakeResolve = null;
    }
    this.#drainFrames();
  }

  #drainFrames(): void {
    while (this.#buffer.length >= 2) {
      const first = this.#buffer[0]!;
      const second = this.#buffer[1]!;
      const fin = (first & 0x80) === 0x80;
      const opcode = first & 0x0f;
      let length = second & 0x7f;
      let offset = 2;
      if (length === 126) {
        if (this.#buffer.length < 4) return;
        length = this.#buffer.readUInt16BE(2);
        offset = 4;
      } else if (length === 127) {
        if (this.#buffer.length < 10) return;
        length = Number(this.#buffer.readBigUInt64BE(2));
        offset = 10;
      }
      if (this.#buffer.length < offset + length) return;

      const payload = this.#buffer.subarray(offset, offset + length);
      this.#buffer = this.#buffer.subarray(offset + length);
      if (!fin) continue;

      if (opcode === 0x8) {
        const code = payload.length >= 2 ? payload.readUInt16BE(0) : 1005;
        const reason = payload.length > 2 ? payload.subarray(2).toString("utf8") : "";
        this.closed = { code, reason };
        this.#closeResolve?.(this.closed);
        this.#closeResolve = null;
        return;
      }
      if (opcode === 0x1) {
        this.#textResolve?.(payload.toString("utf8"));
        this.#textResolve = null;
      }
    }
  }
}
