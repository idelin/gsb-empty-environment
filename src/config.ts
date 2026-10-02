export interface SimConfig {
  nodeIds: string[];
  /** 租约时长（tick） */
  leaseDuration: number;
  /** 主->从 消息单向延迟（tick） */
  forwardLatency: number;
  /** 从->主 应答单向延迟（tick） */
  ackLatency: number;
  /** 时钟漂移上界（tick），用于写栅栏 */
  maxClockDrift: number;
  /** 心跳间隔（tick） */
  heartbeatInterval: number;
  /** 候选者重试间隔（tick） */
  campaignRetryInterval: number;
  /** 各节点时钟偏移（|offset| <= maxClockDrift） */
  clockOffsets: Record<string, number>;
  /** 各节点选举超时（tick），确定性模拟随机退避 */
  electionTimeouts: Record<string, number>;
}

export const DEFAULT_CONFIG: SimConfig = {
  nodeIds: ['A', 'B', 'C'],
  leaseDuration: 45,
  forwardLatency: 2,
  ackLatency: 3,
  maxClockDrift: 2,
  heartbeatInterval: 10,
  campaignRetryInterval: 5,
  clockOffsets: { A: 0, B: 0, C: 1 },
  electionTimeouts: { A: 45, B: 45, C: 50 },
};

export function majorityOf(config: SimConfig): number {
  return Math.floor(config.nodeIds.length / 2) + 1;
}
