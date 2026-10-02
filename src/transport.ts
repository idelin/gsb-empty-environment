import net from 'node:net';
import type { Message, NodeAddress } from './types.ts';

export type RespondFn = (reply: Message) => void;
export type MessageHandler = (msg: Message, respond: RespondFn) => void;

export interface Transport {
  readonly address: NodeAddress | null;
  listen(host: string, port: number): Promise<NodeAddress>;
  onMessage(handler: MessageHandler): void;
  request(addr: NodeAddress, msg: Message, timeoutMs: number): Promise<Message | null>;
  close(): Promise<void>;
}

export class TcpTransport implements Transport {
  private server: net.Server | null = null;
  private readonly sockets = new Set<net.Socket>();
  private handler: MessageHandler | null = null;
  address: NodeAddress | null = null;

  listen(host: string, port: number): Promise<NodeAddress> {
    this.server = net.createServer((socket) => {
      this.sockets.add(socket);
      socket.on('close', () => this.sockets.delete(socket));
      socket.on('error', () => {});
      socket.setEncoding('utf8');
      let buffer = '';
      socket.on('data', (chunk: string) => {
        buffer += chunk;
        let idx = buffer.indexOf('\n');
        while (idx >= 0) {
          const line = buffer.slice(0, idx);
          buffer = buffer.slice(idx + 1);
          idx = buffer.indexOf('\n');
          if (!line.trim()) continue;
          let msg: Message;
          try {
            msg = JSON.parse(line) as Message;
          } catch {
            continue;
          }
          this.handler?.(msg, (reply) => {
            if (!socket.destroyed) socket.write(JSON.stringify(reply) + '\n');
          });
        }
      });
    });
    return new Promise((resolve, reject) => {
      this.server!.once('error', reject);
      this.server!.listen(port, host, () => {
        const bound = this.server!.address() as net.AddressInfo;
        this.address = { host, port: bound.port };
        resolve(this.address);
      });
    });
  }

  onMessage(handler: MessageHandler): void {
    this.handler = handler;
  }

  request(addr: NodeAddress, msg: Message, timeoutMs: number): Promise<Message | null> {
    return new Promise((resolve) => {
      const socket = net.createConnection(addr.port, addr.host);
      let settled = false;
      const finish = (value: Message | null) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        socket.destroy();
        resolve(value);
      };
      const timer = setTimeout(() => finish(null), timeoutMs);
      let buffer = '';
      socket.setEncoding('utf8');
      socket.on('connect', () => socket.write(JSON.stringify(msg) + '\n'));
      socket.on('error', () => finish(null));
      socket.on('end', () => finish(null));
      socket.on('data', (chunk: string) => {
        buffer += chunk;
        const idx = buffer.indexOf('\n');
        if (idx < 0) return;
        try {
          finish(JSON.parse(buffer.slice(0, idx)) as Message);
        } catch {
          finish(null);
        }
      });
    });
  }

  async close(): Promise<void> {
    const server = this.server;
    this.server = null;
    for (const socket of this.sockets) socket.destroy();
    this.sockets.clear();
    if (!server) return;
    await new Promise<void>((resolve) => {
      server.close(() => resolve());
      server.closeAllConnections?.();
    });
  }
}
