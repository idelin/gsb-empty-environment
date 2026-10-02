/**
 * 线协议消息类型。
 *
 * 心跳用并行数组（members/counters）而非对象数组，
 * 显著减少高频率小对象分配。
 */

/**
 * 分层 Gossip 摘要：
 * - members/counters：本 Cell 成员心跳（intra）或本 Cell 聚合心跳（inter），
 *   members 是 Cell 成员列表的共享引用，counters 与之一一对应。
 * - remoteIds/remoteCounters：最近学到的跨 Cell 心跳增量（有界，轮转挑选），
 *   仅在 Cell 内再扩散，避免全网泛洪。
 * - data：广播数据（key -> version），按版本号合并。
 */
export interface DigestMessage {
  kind: 'intra' | 'inter';
  from: number;
  members: number[];
  counters: number[];
  remoteIds: number[];
  remoteCounters: number[];
  data: Array<[string, number]>;
}

/** 扁平基线：全网广播心跳。 */
export interface FlatHeartbeatMessage {
  from: number;
  counter: number;
  data: Array<[string, number]>;
}
