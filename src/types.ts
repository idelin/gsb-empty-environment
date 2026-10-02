/** 日志条目种类：外部输入（网络包到达）与时钟中断（超时） */
export type EventKind = 'packet-arrival' | 'timeout';

export interface LogEntry {
  /** 全局事件序号，从 1 开始 */
  index: number;
  /** 虚拟时钟 tick */
  tick: number;
  kind: EventKind;
  payload: Record<string, number>;
}

export interface TraceHeader {
  /** 主随机种子 —— 重放的一切确定性来源 */
  seed: number;
  /** 环境随机流种子（由 seed 派生，记录于此以便审计） */
  envSeed: number;
  lossProb: number;
  timeoutTicks: number;
  arrivalCount: number;
}

/** 完整轨迹日志：头部（含随机种子）+ 有序事件序列 */
export interface TraceLog {
  header: TraceHeader;
  entries: LogEntry[];
}

/** 一次快照：规范化字节串 + 哈希 */
export interface Snapshot {
  /** 对应已消费的事件序号（0 = 初始状态） */
  afterEvent: number;
  tick: number;
  canonical: string;
  hash: string;
}
