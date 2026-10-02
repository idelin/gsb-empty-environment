export interface NodeRequest {
  key: string;
  payload?: unknown;
}

export interface NodeResponse {
  nodeId: string;
  key: string;
  /** 该请求在节点上看到的最大并发数（用于验证互不阻塞） */
  observedConcurrency: number;
  processedAt: number;
}

/**
 * 存储节点：独立处理请求，节点之间互不影响。
 * latencyMs 模拟该节点的处理耗时，用于验证并发隔离。
 */
export class StorageNode {
  readonly nodeId: string;
  private readonly latencyMs: number;
  private active = 0;
  private maxConcurrent = 0;

  constructor(nodeId: string, latencyMs: number = 0) {
    this.nodeId = nodeId;
    this.latencyMs = latencyMs;
  }

  get maxObservedConcurrency(): number {
    return this.maxConcurrent;
  }

  async handle(req: NodeRequest): Promise<NodeResponse> {
    this.active++;
    this.maxConcurrent = Math.max(this.maxConcurrent, this.active);
    try {
      if (this.latencyMs > 0) {
        await new Promise((r) => setTimeout(r, this.latencyMs));
      }
      return {
        nodeId: this.nodeId,
        key: req.key,
        observedConcurrency: this.maxConcurrent,
        processedAt: Date.now(),
      };
    } finally {
      this.active--;
    }
  }
}
