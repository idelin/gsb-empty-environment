export interface HeartbeatRequest {
  workerId: string;
  /** 本窗口内累计已消耗的 token 数（用量上报，用于 Master 判断空闲度） */
  usedTotal: number;
  /** 本次希望批量预分配的 token 数 */
  requested: number;
}

export interface HeartbeatResponse {
  /** 本窗口内该 Worker 当前持有的配额总量（权威值，含被调拨后的结果） */
  allocated: number;
  /** 租约过期时间（即当前窗口结束时间） */
  expiresAt: number;
  /** 当前滑动窗口起点，用于 Worker 检测窗口切换 */
  windowStart: number;
  /** 全池剩余未分配额度（观测用） */
  poolFree: number;
}

export interface HeartbeatHandler {
  handleHeartbeat(req: HeartbeatRequest): HeartbeatResponse;
}

/**
 * 进程内模拟 RPC 通道：统计 RPC 往返次数，可选注入网络延迟。
 * 换成 HTTP/gRPC 时只需保持 heartbeat() 签名不变。
 */
export class InMemoryTransport {
  rpcCount = 0;
  private readonly master: HeartbeatHandler;
  private readonly latencyMs: number;

  constructor(master: HeartbeatHandler, latencyMs = 0) {
    this.master = master;
    this.latencyMs = latencyMs;
  }

  async heartbeat(req: HeartbeatRequest): Promise<HeartbeatResponse> {
    this.rpcCount += 1;
    if (this.latencyMs > 0) {
      await new Promise((resolve) => setTimeout(resolve, this.latencyMs));
    }
    return this.master.handleHeartbeat(req);
  }
}
