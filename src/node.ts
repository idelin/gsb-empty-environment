import { MatrixClock } from "./matrix-clock.ts";
import type { MatrixSnapshot } from "./matrix-clock.ts";

/** 一条已持久化的日志事件。LSN 在事件源节点上单调递增。 */
export interface LogEntry {
  lsn: number;
  origin: string;
  payload: string;
}

/** 节点间消息：携带发送方的完整矩阵时钟快照 + 日志副本（复制用）。 */
export interface Message {
  from: string;
  matrix: MatrixSnapshot;
  entries: LogEntry[];
}

export interface GcResult {
  /** 每个事件源被截断到的最高 LSN（含）。 */
  truncatedUpTo: Map<string, number>;
  /** 本次被物理移除的日志条目。 */
  removed: LogEntry[];
}

/**
 * 集群节点：维护本地日志、矩阵时钟，并基于矩阵时钟执行
 * 分布式垃圾回收（物理日志截断）。
 */
export class Node {
  readonly id: string;
  readonly clock: MatrixClock;
  readonly log: LogEntry[] = [];
  private lsn = 0;
  private truncatedUpTo = new Map<string, number>();

  constructor(id: string, cluster: string[]) {
    this.id = id;
    this.clock = new MatrixClock(cluster, id);
  }

  /** 产生一条本地事件：推进时钟、分配 LSN、追加日志。 */
  localEvent(payload: string): LogEntry {
    this.clock.tick();
    this.lsn += 1;
    const entry: LogEntry = { lsn: this.lsn, origin: this.id, payload };
    this.log.push(entry);
    return entry;
  }

  /** 向对端发送消息：矩阵快照 + 全量日志副本。 */
  send(): Message {
    return {
      from: this.id,
      matrix: this.clock.snapshot(),
      entries: this.log.map((e) => ({ ...e })),
    };
  }

  /** 接收消息：合并矩阵时钟，幂等地合并日志并更新本地确认。 */
  receive(msg: Message): void {
    if (msg.from === this.id) throw new Error("cannot receive from self");
    this.clock.merge(msg.matrix);
    for (const entry of msg.entries) {
      if (entry.origin === this.id) continue;
      const dup = this.log.some(
        (e) => e.origin === entry.origin && e.lsn === entry.lsn,
      );
      if (!dup) this.log.push({ ...entry });
      this.clock.acknowledge(entry.origin, entry.lsn);
    }
    this.log.sort(
      (a, b) => a.origin.localeCompare(b.origin) || a.lsn - b.lsn,
    );
  }

  /** 当前对 origin 事件的垃圾回收水位线（所有节点均已确认知悉的最大 LSN）。 */
  gcWatermark(originId: string): number {
    return this.clock.gcWatermark(originId);
  }

  /**
   * 分布式垃圾回收：对每个事件源，把 LSN <= 水位线的日志条目
   * 从物理日志中截断。单调推进，幂等。
   */
  collectGarbage(): GcResult {
    const removed: LogEntry[] = [];
    for (const origin of this.clock.nodeIds) {
      const watermark = this.clock.gcWatermark(origin);
      const already = this.truncatedUpTo.get(origin) ?? 0;
      if (watermark <= already) continue;
      this.truncatedUpTo.set(origin, watermark);
      for (let i = this.log.length - 1; i >= 0; i--) {
        const e = this.log[i];
        if (e.origin === origin && e.lsn > already && e.lsn <= watermark) {
          removed.push(e);
          this.log.splice(i, 1);
        }
      }
    }
    removed.sort((a, b) => a.origin.localeCompare(b.origin) || a.lsn - b.lsn);
    return { truncatedUpTo: new Map(this.truncatedUpTo), removed };
  }
}
