/**
 * In-memory message transport simulating an asynchronous network.
 *
 * Supports per-link partitioning/healing, configurable latency and full
 * accounting of in-flight messages and pending timers so tests can assert
 * strict quiescence (no leaked work) after every scenario.
 */

export class NetworkError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'NetworkError';
  }
}

export type Handler = (msg: unknown) => unknown | Promise<unknown>;

export class Network {
  private readonly handlers = new Map<number, Handler>();
  private readonly links = new Map<string, boolean>();
  private readonly timers = new Set<ReturnType<typeof setTimeout>>();
  private inFlightCount = 0;
  private idleWaiters: Array<() => void> = [];
  private readonly latency: () => number;

  readonly stats = { sent: 0, delivered: 0, dropped: 0 };

  constructor(latency: () => number = () => 0) {
    this.latency = latency;
  }

  register(id: number, handler: Handler): void {
    this.handlers.set(id, handler);
  }

  private key(a: number, b: number): string {
    return `${a}->${b}`;
  }

  isUp(a: number, b: number): boolean {
    return this.links.get(this.key(a, b)) ?? true;
  }

  setLink(a: number, b: number, up: boolean): void {
    this.links.set(this.key(a, b), up);
  }

  /** Cut all links between the two groups (both directions). */
  partition(groupA: number[], groupB: number[]): void {
    for (const a of groupA) {
      for (const b of groupB) {
        this.setLink(a, b, false);
        this.setLink(b, a, false);
      }
    }
  }

  healAll(): void {
    this.links.clear();
  }

  get inFlight(): number {
    return this.inFlightCount;
  }

  get pendingTimers(): number {
    return this.timers.size;
  }

  private sleep(ms: number): Promise<void> {
    if (ms <= 0) return Promise.resolve();
    return new Promise((resolve) => {
      const t = setTimeout(() => {
        this.timers.delete(t);
        resolve();
        this.checkIdle();
      }, ms);
      this.timers.add(t);
    });
  }

  /** RPC-style request/response. Throws NetworkError when the link is down. */
  async call<T>(from: number, to: number, msg: unknown): Promise<T> {
    this.stats.sent++;
    if (!this.isUp(from, to)) {
      this.stats.dropped++;
      throw new NetworkError(`link down ${from}->${to}`);
    }
    const handler = this.handlers.get(to);
    if (!handler) {
      this.stats.dropped++;
      throw new NetworkError(`no handler registered for node ${to}`);
    }
    this.inFlightCount++;
    try {
      await this.sleep(this.latency());
      if (!this.isUp(from, to)) {
        this.stats.dropped++;
        throw new NetworkError(`link down ${from}->${to} (message lost in flight)`);
      }
      this.stats.delivered++;
      return (await handler(msg)) as T;
    } finally {
      this.inFlightCount--;
      this.checkIdle();
    }
  }

  /** Fire-and-forget delivery, still tracked by inFlight/drain. */
  notify(from: number, to: number, msg: unknown): void {
    void this.call(from, to, msg).catch(() => {
      /* best-effort delivery; loss is tolerated by the protocol */
    });
  }

  private checkIdle(): void {
    if (this.inFlightCount === 0 && this.timers.size === 0 && this.idleWaiters.length > 0) {
      const waiters = this.idleWaiters;
      this.idleWaiters = [];
      for (const resolve of waiters) resolve();
    }
  }

  /** Resolves once the network is fully quiescent (no in-flight msgs, no timers). */
  async drain(): Promise<void> {
    while (this.inFlightCount > 0 || this.timers.size > 0) {
      await new Promise<void>((resolve) => {
        this.idleWaiters.push(resolve);
        this.checkIdle();
      });
    }
  }
}
