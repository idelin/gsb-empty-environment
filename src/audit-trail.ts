/**
 * 审计追踪器（黑匣子）：只追加的结构化事件流水，
 * 支持按逻辑时间轴回放与因果链追溯。
 */
import { compareLogical, type LogicalTimestamp } from './logical-clock.ts';
import type { AuditEvent, EventKind } from './events.ts';

export interface RecordInput {
  kind: EventKind;
  leaseId: string;
  actorId: string;
  clock: LogicalTimestamp;
  causedBy?: number[];
  details?: Record<string, unknown>;
}

export class AuditTrail {
  private readonly events: AuditEvent[] = [];
  private readonly now: () => number;

  /**
   * @param now 墙钟来源（可注入，便于在测试中复现"同一毫秒"的竞态）
   */
  constructor(now: () => number = Date.now) {
    this.now = now;
  }

  /** 追加一条事件，返回固化后的事件记录 */
  record(input: RecordInput): AuditEvent {
    const event: AuditEvent = {
      seq: this.events.length + 1,
      kind: input.kind,
      leaseId: input.leaseId,
      actorId: input.actorId,
      clock: input.clock,
      wallTimeMs: this.now(),
      causedBy: input.causedBy ?? [],
      details: input.details ?? {},
    };
    this.events.push(event);
    return event;
  }

  /** 全部事件（按记录次序，即仲裁器观察到的到达次序） */
  all(): readonly AuditEvent[] {
    return this.events;
  }

  bySeq(seq: number): AuditEvent {
    const event = this.events[seq - 1];
    if (!event) throw new RangeError(`no event with seq=${seq}`);
    return event;
  }

  forLease(leaseId: string): AuditEvent[] {
    return this.events.filter((e) => e.leaseId === leaseId);
  }

  /**
   * 时间轴回放：按逻辑时间戳全序（lamport, nodeId）排序，
   * 同值时以记录序号兜底，得到确定性的因果一致回放序列。
   */
  timeline(leaseId?: string): AuditEvent[] {
    const source = leaseId === undefined ? [...this.events] : this.forLease(leaseId);
    return source.sort((a, b) => compareLogical(a.clock, b.clock) || a.seq - b.seq);
  }

  /**
   * 因果证据链：从给定事件沿 causedBy 回溯到根因，
   * 返回从根因到该事件的有序链条。
   */
  causalChain(seq: number): AuditEvent[] {
    const chain: AuditEvent[] = [];
    const visited = new Set<number>();
    const visit = (s: number): void => {
      if (visited.has(s)) return;
      visited.add(s);
      const event = this.bySeq(s);
      for (const cause of event.causedBy) visit(cause);
      chain.push(event);
    };
    visit(seq);
    return chain;
  }

  /** 校验因果引用完整性：所有 causedBy 均存在且严格小于自身 seq */
  verifyCausalIntegrity(): string[] {
    const violations: string[] = [];
    for (const event of this.events) {
      for (const cause of event.causedBy) {
        if (cause < 1 || cause >= event.seq) {
          violations.push(
            `event#${event.seq}(${event.kind}) has invalid cause seq=${cause}`,
          );
        }
      }
    }
    return violations;
  }
}
