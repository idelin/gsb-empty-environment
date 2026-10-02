import type { Message, NodeAddress } from './types.ts';
import type { MessageHandler, Transport } from './transport.ts';

export class InMemoryHub {
  private readonly peers = new Map<string, InMemoryTransport>();
  private nextPort = 10000;

  allocatePort(): number {
    return this.nextPort++;
  }

  register(address: NodeAddress, transport: InMemoryTransport): void {
    this.peers.set(`${address.host}:${address.port}`, transport);
  }

  unregister(address: NodeAddress): void {
    this.peers.delete(`${address.host}:${address.port}`);
  }

  resolve(address: NodeAddress): InMemoryTransport | undefined {
    return this.peers.get(`${address.host}:${address.port}`);
  }
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

export class InMemoryTransport implements Transport {
  private handler: MessageHandler | null = null;
  address: NodeAddress | null = null;
  private readonly hub: InMemoryHub;

  constructor(hub: InMemoryHub) {
    this.hub = hub;
  }

  listen(host: string, port: number): Promise<NodeAddress> {
    const actualPort = port === 0 ? this.hub.allocatePort() : port;
    this.address = { host, port: actualPort };
    this.hub.register(this.address, this);
    return Promise.resolve(this.address);
  }

  onMessage(handler: MessageHandler): void {
    this.handler = handler;
  }

  deliver(msg: Message, respond: (reply: Message) => void): void {
    this.handler?.(msg, respond);
  }

  request(addr: NodeAddress, msg: Message, timeoutMs: number): Promise<Message | null> {
    return new Promise((resolve) => {
      const peer = this.hub.resolve(addr);
      let settled = false;
      const finish = (value: Message | null) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        resolve(value);
      };
      const timer = setTimeout(() => finish(null), timeoutMs);
      if (!peer) {
        finish(null);
        return;
      }
      setImmediate(() => {
        peer.deliver(clone(msg), (reply) => finish(clone(reply)));
      });
    });
  }

  close(): Promise<void> {
    if (this.address) this.hub.unregister(this.address);
    this.address = null;
    return Promise.resolve();
  }
}
