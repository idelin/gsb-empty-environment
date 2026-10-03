/**
 * 分布式节点：维护矩阵时钟 + 本地事件日志（带 LSN），
 * 并基于矩阵时钟的 GC 水位执行物理日志截断。
 */
import { MatrixClock, type Vector } from './matrix-clock.ts';
import type { Cluster } from './cluster.ts';

export type EventKind = 'local' | 'send' | 'receive';

export interface LogRecord {
  lsn: number;
  kind: EventKind;
  description: string;
  /** 该事件的向量时间戳。 */
  timestamp: Vector;
}

export interface Message {
  from: number;
  to: number;
  payload: string;
  /** 发送事件的向量时间戳。 */
  timestamp: Vector;
  /** 发送方的矩阵时钟快照。 */
  matrix: number[][];
}

export class Node {
  readonly id: number;
  readonly name: string;
  readonly clock: MatrixClock;
  private cluster!: Cluster;
  private log: LogRecord[] = [];
  private nextLsn = 1;
  /** 已物理截断到的 LSN（含）。 */
  truncatedUpTo = 0;

  constructor(id: number, size: number, name: string) {
    this.id = id;
    this.name = name;
    this.clock = new MatrixClock(id, size);
  }

  attach(cluster: Cluster): void {
    this.cluster = cluster;
  }

  localEvent(description: string): void {
    const ts = this.clock.tick();
    this.append('local', description, ts);
  }

  send(to: number, payload: string): void {
    const ts = this.clock.tick();
    this.append('send', `-> ${this.cluster.nameOf(to)}: ${payload}`, ts);
    this.cluster.deliver({
      from: this.id,
      to,
      payload,
      timestamp: ts,
      matrix: this.clock.matrix(),
    });
  }

  onMessage(msg: Message): void {
    const ts = this.clock.receive(msg.from, msg.matrix);
    this.append('receive', `<- ${this.cluster.nameOf(msg.from)}: ${msg.payload}`, ts);
  }

  private append(kind: EventKind, description: string, timestamp: Vector): void {
    this.log.push({ lsn: this.nextLsn++, kind, description, timestamp: [...timestamp] });
  }

  /** 当前仍驻留在物理日志中的记录（截断后剩余的）。 */
  records(): readonly LogRecord[] {
    return this.log;
  }

  /**
   * 计算可安全回收的 LSN 前缀边界：
   * 从日志头部开始，凡时间戳被矩阵时钟 GC 水位支配的记录都可回收；
   * 物理截断要求连续前缀，因此遇到第一条未被覆盖的记录即停止。
   */
  computeGcLsn(): number {
    const horizon = this.clock.horizon();
    let safe = 0;
    for (const rec of this.log) {
      if (!MatrixClock.coveredBy(rec.timestamp, horizon)) break;
      safe = rec.lsn;
    }
    return safe;
  }

  /**
   * 触发底层物理日志截断：丢弃所有 LSN <= 截断点 的历史记录。
   * 截断点 = min(矩阵时钟安全水位, upToLsn)——绝不会越过安全水位，
   * 保证只有「已被全网每个节点确认知悉」的历史才会被物理删除。
   * 返回本次截断到的 LSN。
   */
  truncateLog(upToLsn: number = Number.POSITIVE_INFINITY): number {
    const target = Math.min(this.computeGcLsn(), upToLsn);
    if (target <= this.truncatedUpTo) return this.truncatedUpTo;
    this.log = this.log.filter((rec) => rec.lsn > target);
    this.truncatedUpTo = target;
    return target;
  }
}
