import { SleepFn, defaultSleep } from "./policies";

export interface ReadResult {
  nodeId: string;
  partition: string;
  version: number;
  latencyMs: number;
}

export interface SimulatedNodeOptions {
  /** Fixed latency, or [min, max] sampled uniformly per read. */
  latencyMs: number | readonly [number, number];
  /** Initial version per partition hosted by this node. */
  partitions: Record<string, number>;
  /** Injectable sleep for deterministic tests. */
  sleep?: SleepFn;
}

/**
 * A simulated replica node. Each node hosts partitions at independent
 * versions, so replication lag is modeled by advancing versions on
 * some nodes but not others. Every read pays the node's latency.
 */
export class SimulatedNode {
  readonly id: string;
  private readonly latencyMs: number | readonly [number, number];
  private readonly partitions: Map<string, number>;
  private readonly sleep: SleepFn;

  constructor(id: string, options: SimulatedNodeOptions) {
    this.id = id;
    this.latencyMs = options.latencyMs;
    this.partitions = new Map<string, number>(Object.entries(options.partitions));
    this.sleep = options.sleep || defaultSleep;
  }

  /** Current version this replica holds for a partition. */
  currentVersion(partition: string): number {
    return this.partitions.get(partition) || 0;
  }

  /** Simulate replication (or a write) advancing this replica. */
  advance(partition: string, version: number): void {
    if (version > this.currentVersion(partition)) {
      this.partitions.set(partition, version);
    }
  }

  async read(partition: string): Promise<ReadResult> {
    const latency = this.sampleLatency();
    await this.sleep(latency);
    return {
      nodeId: this.id,
      partition,
      version: this.currentVersion(partition),
      latencyMs: latency,
    };
  }

  private sampleLatency(): number {
    if (typeof this.latencyMs === "number") {
      return this.latencyMs;
    }
    const [min, max] = this.latencyMs;
    return min + Math.random() * (max - min);
  }
}
