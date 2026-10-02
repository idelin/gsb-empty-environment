/**
 * Lamport 逻辑时钟：为分布式事件提供可比较的逻辑时间戳。
 * 当两个节点的物理时钟（毫秒级）无法区分先后时，
 * 逻辑时间戳 + 节点标识给出确定性的全序。
 */

export interface LogicalTimestamp {
  /** Lamport 计数器值 */
  lamport: number;
  /** 产生该时间戳的节点标识（用于同值时的确定性仲裁） */
  nodeId: string;
}

export class LamportClock {
  private counter = 0;
  private readonly nodeId: string;

  constructor(nodeId: string) {
    this.nodeId = nodeId;
  }

  /** 本地事件：计数器 +1 并返回新时间戳 */
  tick(): LogicalTimestamp {
    this.counter += 1;
    return { lamport: this.counter, nodeId: this.nodeId };
  }

  /** 观察到远端时间戳：取 max 后 +1（Lamport 规则） */
  observe(remote: LogicalTimestamp): LogicalTimestamp {
    this.counter = Math.max(this.counter, remote.lamport) + 1;
    return { lamport: this.counter, nodeId: this.nodeId };
  }
}

/** 逻辑时间戳全序比较：先比 lamport，再比 nodeId */
export function compareLogical(a: LogicalTimestamp, b: LogicalTimestamp): number {
  if (a.lamport !== b.lamport) return a.lamport - b.lamport;
  if (a.nodeId === b.nodeId) return 0;
  return a.nodeId < b.nodeId ? -1 : 1;
}

export function formatLogical(ts: LogicalTimestamp): string {
  return `L(${ts.lamport}@${ts.nodeId})`;
}
