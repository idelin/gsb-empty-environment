import { Readable } from "node:stream";
import { Duplex } from "node:stream";

/** A pair of connected in-memory sockets: data written to one is read from the other. */
export function createSocketPair(): { client: Duplex; server: Duplex } {
  class MockSocket extends Duplex {
    peer: MockSocket | null = null;
    override _read(): void {}
    override _write(chunk: Buffer, _enc: BufferEncoding, cb: () => void): void {
      this.peer?.push(chunk);
      cb();
    }
    override _final(cb: () => void): void {
      this.peer?.push(null);
      cb();
    }
    override _destroy(_err: Error | null, cb: (err: Error | null) => void): void {
      const peer = this.peer;
      if (peer && !peer.destroyed) queueMicrotask(() => peer.destroy());
      cb(null);
    }
  }
  const client = new MockSocket();
  const server = new MockSocket();
  client.peer = server;
  server.peer = client;
  return { client, server };
}

export interface MockHttpResponse {
  readonly status: number;
  readonly headers: Record<string, string>;
  readonly body: string;
  json(): unknown;
}

export interface MockRequestOptions {
  readonly method: string;
  readonly path: string;
  readonly headers?: Record<string, string>;
  readonly body?: string;
}

/** Drive the service's HTTP request handler in-process, without a listening socket. */
export function httpRequest(
  server: { emit(event: "request", req: unknown, res: unknown): boolean },
  options: MockRequestOptions,
): Promise<MockHttpResponse> {
  return new Promise((resolve, reject) => {
    const req = Readable.from(options.body ? [Buffer.from(options.body)] : []) as Readable & {
      method?: string;
      url?: string;
      headers?: Record<string, string>;
    };
    req.method = options.method;
    req.url = options.path;
    req.headers = options.headers ?? {};

    let status = 200;
    const headers: Record<string, string> = {};
    const chunks: Buffer[] = [];
    const res = {
      writeHead(code, hdrs) {
        status = code;
        Object.assign(headers, hdrs ?? {});
      },
      end(data) {
        if (data) chunks.push(Buffer.from(data));
        const body = Buffer.concat(chunks).toString("utf8");
        resolve({
          status,
          headers,
          body,
          json: () => JSON.parse(body),
        });
      },
    };

    if (!server.emit("request", req, res)) {
      reject(new Error("no request listener registered"));
    }
  });
}
