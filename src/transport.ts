/** 网络传输抽象：主节点与从节点之间唯一的网络通道。 */
export interface HeartbeatRequest {
  readonly term: number;
  readonly leaderId: string;
  readonly requestedLeaseMs: number;
}

export interface HeartbeatReply {
  readonly followerId: string;
  readonly ok: boolean;
  /** 从节点承诺：在该本地时间点之前不为其他主节点投票。 */
  readonly promisedUntil: number;
}

export interface Transport {
  sendHeartbeat(followerId: string, req: HeartbeatRequest): Promise<HeartbeatReply>;
  /** 已发生的真实网络调用次数（用于验证“零网络开销”）。 */
  readonly networkCallCount: number;
}

export interface SimulatedTransportOptions {
  /** 单程网络延迟（毫秒）。 */
  latencyMs?: number;
  /** 被隔离（不可达）的从节点集合。 */
  partitioned?: ReadonlySet<string>;
  clock?: { now(): number };
}

/** 内存模拟传输层：带延迟、故障注入与调用计数。 */
export class SimulatedTransport implements Transport {
  private latencyMs: number;
  private partitioned: Set<string>;
  private clock: { now(): number };
  private calls = 0;
  private handlers = new Map<string, (req: HeartbeatRequest) => HeartbeatReply>();

  constructor(options: SimulatedTransportOptions = {}) {
    this.latencyMs = options.latencyMs ?? 1;
    this.partitioned = new Set(options.partitioned ?? []);
    this.clock = options.clock ?? { now: () => Date.now() };
  }

  get networkCallCount(): number {
    return this.calls;
  }

  register(followerId: string, handler: (req: HeartbeatRequest) => HeartbeatReply): void {
    this.handlers.set(followerId, handler);
  }

  setLatency(ms: number): void {
    this.latencyMs = ms;
  }

  partition(followerId: string): void {
    this.partitioned.add(followerId);
  }

  heal(followerId: string): void {
    this.partitioned.delete(followerId);
  }

  sendHeartbeat(followerId: string, req: HeartbeatRequest): Promise<HeartbeatReply> {
    this.calls++;
    const handler = this.handlers.get(followerId);
    return new Promise((resolve, reject) => {
      setTimeout(() => {
        if (this.partitioned.has(followerId)) {
          reject(new Error(`follower ${followerId} unreachable (partitioned)`));
          return;
        }
        if (!handler) {
          reject(new Error(`follower ${followerId} not registered`));
          return;
        }
        resolve(handler(req));
      }, this.latencyMs);
    });
  }
}
