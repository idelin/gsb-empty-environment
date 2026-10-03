export interface HeartbeatRequest {
  term: number;
  /** 主节点要求从节点承诺不接纳新主的截止时间（主节点本地时钟基准） */
  promisedUntil: number;
}

export interface HeartbeatAck {
  followerId: string;
  term: number;
  ok: boolean;
}

/**
 * 模拟从节点副本：带网络时延的心跳响应，可模拟网络分区。
 * 统计心跳次数用于验证“安全窗口内零网络开销”。
 */
export class FollowerReplica {
  readonly id: string;
  private readonly latencyMs: number;
  private partitioned = false;
  private heartbeatCount = 0;
  private promisedUntil = Number.NEGATIVE_INFINITY;

  constructor(id: string, latencyMs = 1) {
    this.id = id;
    this.latencyMs = latencyMs;
  }

  partition(): void {
    this.partitioned = true;
  }

  heal(): void {
    this.partitioned = false;
  }

  getHeartbeatCount(): number {
    return this.heartbeatCount;
  }

  async onHeartbeat(req: HeartbeatRequest): Promise<HeartbeatAck> {
    if (this.partitioned) {
      return new Promise((_, reject) =>
        setTimeout(() => reject(new Error(`follower ${this.id} unreachable`)), this.latencyMs),
      );
    }
    return new Promise((resolve) =>
      setTimeout(() => {
        this.heartbeatCount += 1;
        this.promisedUntil = Math.max(this.promisedUntil, req.promisedUntil);
        resolve({ followerId: this.id, term: req.term, ok: true });
      }, this.latencyMs),
    );
  }
}
