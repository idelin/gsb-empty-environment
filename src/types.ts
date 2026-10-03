/** Real-time load metrics reported by a node, each in the range [0, 1]. */
export interface NodeMetrics {
  cpu: number;
  memory: number;
}

export interface NodeSnapshot {
  id: string;
  metrics: NodeMetrics;
  /** Effective load in [0, 1]: the bottleneck dimension (max of cpu/memory). */
  effectiveLoad: number;
  /** Health factor in [minFactor, 1]; 1 means fully healthy. */
  healthFactor: number;
  /** Number of virtual nodes this node currently owns on the ring. */
  weight: number;
}
