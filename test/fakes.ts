import type { Connection, Dialer } from "../src/pool.ts";
import type { Resolution, Resolver } from "../src/resolver.ts";
import { canonicalIp } from "../src/address.ts";

export class FakeConnection implements Connection {
  closed = false;
  readonly remoteAddress: string;
  readonly remotePort: number;

  constructor(remoteAddress: string, remotePort: number) {
    this.remoteAddress = remoteAddress;
    this.remotePort = remotePort;
  }

  close(): void {
    this.closed = true;
  }
}

interface PendingDial {
  address: string;
  port: number;
  resolve: (conn: Connection) => void;
  reject: (err: unknown) => void;
}

export class FakeDialer implements Dialer {
  readonly pending: PendingDial[] = [];
  readonly attempts: { address: string; port: number }[] = [];

  dial(address: string, port: number): Promise<Connection> {
    this.attempts.push({ address, port });
    return new Promise<Connection>((resolve, reject) => {
      this.pending.push({ address, port, resolve, reject });
    });
  }

  private findPending(address: string): number {
    const wanted = canonicalIp(address) ?? address;
    return this.pending.findIndex(p => (canonicalIp(p.address) ?? p.address) === wanted);
  }

  succeed(address: string, overrideAddress?: string): FakeConnection {
    const index = this.findPending(address);
    if (index < 0) throw new Error(`no pending dial for ${address}`);
    const [dial] = this.pending.splice(index, 1);
    const conn = new FakeConnection(overrideAddress ?? dial.address, dial.port);
    dial.resolve(conn);
    return conn;
  }

  fail(address: string, err: unknown): void {
    const index = this.findPending(address);
    if (index < 0) throw new Error(`no pending dial for ${address}`);
    const [dial] = this.pending.splice(index, 1);
    dial.reject(err);
  }
}

export class StaticResolver implements Resolver {
  private readonly table = new Map<string, Resolution>();
  readonly queries: string[] = [];

  add(hostname: string, resolution: Resolution): void {
    this.table.set(hostname, resolution);
  }

  resolve(hostname: string): Promise<Resolution> {
    this.queries.push(hostname);
    const resolution = this.table.get(hostname);
    if (!resolution) return Promise.reject(new Error(`NXDOMAIN: ${hostname}`));
    return Promise.resolve(resolution);
  }
}

export function flush(): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, 0));
}
