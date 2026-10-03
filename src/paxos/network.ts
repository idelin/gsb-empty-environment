import type { MessageType } from './types.ts';

export class NetworkError extends Error {
  readonly from: number;
  readonly to: number;

  constructor(from: number, to: number) {
    super(`node ${to} unreachable from node ${from}`);
    this.name = 'NetworkError';
    this.from = from;
    this.to = to;
  }
}

export type RpcHandler = (type: MessageType, payload: unknown, from: number) => Promise<unknown>;

/**
 * Deterministic in-memory network: messages are delivered on the microtask
 * queue (no real timers), directed links can be cut to simulate partitions,
 * and `inFlight` exposes pending deliveries for strict leak assertions.
 */
export class SimulatedNetwork {
  #handlers = new Map<number, RpcHandler>();
  #blocked = new Set<string>();
  #inFlight = 0;
  #sent = new Map<MessageType, number>();
  #dropFilter: DropFilter | null = null;

  /** Install/remove a fine-grained drop hook (tests only). */
  setDropFilter(filter: DropFilter | null): void {
    this.#dropFilter = filter;
  }

  register(id: number, handler: RpcHandler): void {
    this.#handlers.set(id, handler);
  }

  unregister(id: number): void {
    this.#handlers.delete(id);
  }

  #key(a: number, b: number): string {
    return `${a}->${b}`;
  }

  /** Cut every directed link between the two groups (both directions). */
  partition(groupA: number[], groupB: number[]): void {
    for (const a of groupA) {
      for (const b of groupB) {
        this.#blocked.add(this.#key(a, b));
        this.#blocked.add(this.#key(b, a));
      }
    }
  }

  /** Cut one directed link (asymmetric faults). */
  cut(from: number, to: number): void {
    this.#blocked.add(this.#key(from, to));
  }

  heal(): void {
    this.#blocked.clear();
  }

  isBlocked(from: number, to: number): boolean {
    return this.#blocked.has(this.#key(from, to));
  }

  async rpc(from: number, to: number, type: MessageType, payload: unknown): Promise<unknown> {
    if (this.isBlocked(from, to)) throw new NetworkError(from, to);
    if (this.#dropFilter?.(from, to, type, payload)) throw new NetworkError(from, to);
    const handler = this.#handlers.get(to);
    if (!handler) throw new NetworkError(from, to);
    this.#inFlight++;
    this.#sent.set(type, (this.#sent.get(type) ?? 0) + 1);
    try {
      // Yield once so concurrent rpcs interleave like real message passing.
      await Promise.resolve();
      return await handler(type, payload, from);
    } finally {
      this.#inFlight--;
    }
  }

  get inFlight(): number {
    return this.#inFlight;
  }

  sentCount(type: MessageType): number {
    return this.#sent.get(type) ?? 0;
  }

  resetStats(): void {
    this.#sent.clear();
  }
}

/** Test hook: fine-grained message dropping (e.g. lose one slot's accepts). */
export type DropFilter = (from: number, to: number, type: MessageType, payload: unknown) => boolean;
