export interface QueryResult {
  nodeId: string;
  key: string;
  value: string;
  startedAt: number;
  finishedAt: number;
}

/**
 * A storage node. handleQuery simulates I/O latency so concurrent
 * queries on different nodes genuinely overlap.
 */
export class StorageNode {
  readonly nodeId: string;
  private readonly latencyMs: number;
  private active = 0;
  maxConcurrent = 0;
  readonly processedKeys: string[] = [];

  constructor(nodeId: string, latencyMs: number = 50) {
    this.nodeId = nodeId;
    this.latencyMs = latencyMs;
  }

  async handleQuery(key: string): Promise<QueryResult> {
    this.active += 1;
    this.maxConcurrent = Math.max(this.maxConcurrent, this.active);
    const startedAt = Date.now();
    await new Promise((resolve) => setTimeout(resolve, this.latencyMs));
    this.processedKeys.push(key);
    this.active -= 1;
    return {
      nodeId: this.nodeId,
      key,
      value: `value-of(${key})@${this.nodeId}`,
      startedAt,
      finishedAt: Date.now(),
    };
  }
}
