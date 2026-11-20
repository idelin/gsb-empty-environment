import { TenantPolicy } from "./policy.ts";
import type { TenantPolicyConfig } from "./policy.ts";
import type { Resolver } from "./resolver.ts";
import { authorizeAddress } from "./credentials.ts";
import type { Credential } from "./credentials.ts";
import { canonicalIp } from "./address.ts";
import { normalizeHostname } from "./hostname.ts";

export interface Connection {
  readonly remoteAddress: string;
  readonly remotePort: number;
  readonly closed: boolean;
  close(): void;
}

export interface Dialer {
  dial(address: string, port: number): Promise<Connection>;
}

export interface PoolOptions {
  resolver: Resolver;
  dialer: Dialer;
  connectTimeoutMs?: number;
  nowMs?: () => number;
}

interface Authorized {
  credential: Credential;
  chainNames: string[];
}

interface PoolEntry extends Authorized {
  connection: Connection;
}

export class ConnectError extends Error {
  readonly reasons: string[];

  constructor(message: string, reasons: string[]) {
    super(message);
    this.name = "ConnectError";
    this.reasons = reasons;
  }
}

const DEFAULT_CONNECT_TIMEOUT_MS = 5000;

export class ConnectionPool {
  private readonly resolver: Resolver;
  private readonly dialer: Dialer;
  private readonly connectTimeoutMs: number;
  private readonly nowMs: () => number;
  private readonly policies = new Map<string, TenantPolicy>();
  private readonly generations = new Map<string, number>();
  private readonly entries: PoolEntry[] = [];

  constructor(options: PoolOptions) {
    this.resolver = options.resolver;
    this.dialer = options.dialer;
    this.connectTimeoutMs = options.connectTimeoutMs ?? DEFAULT_CONNECT_TIMEOUT_MS;
    this.nowMs = options.nowMs ?? (() => Date.now());
  }

  get connectionCount(): number {
    return this.entries.length;
  }

  policyFor(tenantId: string): TenantPolicy | undefined {
    return this.policies.get(tenantId);
  }

  setPolicy(tenantId: string, config: TenantPolicyConfig): TenantPolicy {
    const generation = (this.generations.get(tenantId) ?? 0) + 1;
    this.generations.set(tenantId, generation);
    const policy = new TenantPolicy(tenantId, config, generation);
    this.policies.set(tenantId, policy);
    const now = this.nowMs();
    for (const entry of [...this.entries]) {
      if (entry.credential.tenantId !== tenantId) continue;
      if (!this.entryUsable(entry, policy, now)) this.removeEntry(entry);
    }
    return policy;
  }

  sweepExpired(): void {
    const now = this.nowMs();
    for (const entry of [...this.entries]) {
      const policy = this.policies.get(entry.credential.tenantId);
      if (!policy || !this.entryUsable(entry, policy, now)) {
        this.removeEntry(entry);
      }
    }
  }

  closeAll(): void {
    for (const entry of [...this.entries]) this.removeEntry(entry);
  }

  async connect(tenantId: string, hostname: string, port: number): Promise<Connection> {
    const policy = this.policies.get(tenantId);
    if (!policy) {
      throw new ConnectError(`no policy registered for tenant "${tenantId}"`, []);
    }
    const name = normalizeHostname(hostname);
    const now = this.nowMs();

    for (const entry of [...this.entries]) {
      const credential = entry.credential;
      if (credential.tenantId !== tenantId) continue;
      if (credential.hostname !== name || credential.port !== port) continue;
      if (this.entryUsable(entry, policy, now)) return entry.connection;
      this.removeEntry(entry);
    }

    const resolution = await this.resolver.resolve(name);
    const candidates: Authorized[] = [];
    const reasons: string[] = [];
    for (const resolved of resolution.addresses) {
      const result = authorizeAddress({
        policy,
        hostname: name,
        port,
        resolution,
        address: resolved.address,
        nowMs: now,
      });
      if (result.ok) {
        candidates.push({
          credential: result.credential,
          chainNames: resolution.chain.map(hop => normalizeHostname(hop.name)),
        });
      } else {
        reasons.push(`${result.reason}: ${result.detail}`);
      }
    }
    if (candidates.length === 0) {
      throw new ConnectError(
        `no authorized address for "${name}" (tenant "${tenantId}")`,
        reasons,
      );
    }

    const winner = await this.handshakeRace(tenantId, candidates, port);
    this.entries.push({ ...winner });
    return winner.connection;
  }

  private async handshakeRace(
    tenantId: string,
    candidates: Authorized[],
    port: number,
  ): Promise<PoolEntry> {
    interface Tagged {
      candidate: Authorized;
      self: Promise<Tagged>;
      connection?: Connection;
      error?: unknown;
    }
    const tags: Tagged[] = candidates.map(candidate => {
      const tag = { candidate } as Tagged;
      tag.self = this.dialer.dial(candidate.credential.address, port).then(
        conn => {
          tag.connection = conn;
          return tag;
        },
        err => {
          tag.error = err;
          return tag;
        },
      );
      return tag;
    });
    const pending = new Set(tags.map(tag => tag.self));
    const errors: string[] = [];
    const deadline = this.nowMs() + this.connectTimeoutMs;
    let winner: Tagged | null = null;
    let timedOut = false;

    while (pending.size > 0) {
      const remaining = deadline - this.nowMs();
      if (remaining <= 0) {
        timedOut = true;
        break;
      }
      let timer: ReturnType<typeof setTimeout> | undefined;
      const timeout = new Promise<null>(resolve => {
        timer = setTimeout(() => resolve(null), remaining);
      });
      const settled = await Promise.race([...pending, timeout]);
      if (timer !== undefined) clearTimeout(timer);
      if (settled === null) {
        timedOut = true;
        break;
      }
      pending.delete(settled.self);
      if (settled.error !== undefined) {
        errors.push(
          settled.error instanceof Error ? settled.error.message : String(settled.error),
        );
        continue;
      }
      const conn = settled.connection as Connection;
      if (this.candidateUsable(tenantId, settled.candidate, conn)) {
        winner = settled;
        break;
      }
      conn.close();
      errors.push(
        `candidate ${settled.candidate.credential.address} lost authorization before winning`,
      );
    }

    for (const tag of tags) {
      if (tag === winner) continue;
      void tag.self.then(settled => {
        settled.connection?.close();
      });
    }

    if (winner) {
      return {
        credential: winner.candidate.credential,
        chainNames: winner.candidate.chainNames,
        connection: winner.connection as Connection,
      };
    }
    const why = timedOut
      ? `handshake timed out after ${this.connectTimeoutMs}ms`
      : "all candidates failed";
    throw new ConnectError(`${why} (tenant "${tenantId}")`, errors);
  }

  private candidateUsable(tenantId: string, candidate: Authorized, conn: Connection): boolean {
    const policy = this.policies.get(tenantId);
    if (!policy) return false;
    if (conn.closed) return false;
    const credential = candidate.credential;
    if (credential.tenantId !== tenantId) return false;
    if (canonicalIp(conn.remoteAddress) !== credential.address) return false;
    if (conn.remotePort !== credential.port) return false;
    return this.stillAuthorized(candidate, policy, this.nowMs());
  }

  private entryUsable(entry: PoolEntry, policy: TenantPolicy, now: number): boolean {
    if (entry.connection.closed) return false;
    if (canonicalIp(entry.connection.remoteAddress) !== entry.credential.address) return false;
    if (entry.connection.remotePort !== entry.credential.port) return false;
    return this.stillAuthorized(entry, policy, now);
  }

  private stillAuthorized(auth: Authorized, policy: TenantPolicy, now: number): boolean {
    const credential = auth.credential;
    if (credential.expiresAtMs <= now) return false;
    if (!policy.isAddressAllowed(credential.address)) return false;
    if (!policy.allowsHostname(credential.hostname)) return false;
    for (const hop of auth.chainNames) {
      if (!policy.allowsHostname(hop)) return false;
    }
    return true;
  }

  private removeEntry(entry: PoolEntry): void {
    const index = this.entries.indexOf(entry);
    if (index >= 0) this.entries.splice(index, 1);
    if (!entry.connection.closed) entry.connection.close();
  }
}
