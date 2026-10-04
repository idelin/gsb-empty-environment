import type { EventServer, ServerMessage } from './server.ts';
import {
  stateKey,
  type EventRecord,
  type Filter,
  type StateEntry,
} from './types.ts';

export interface Gap {
  expected: number;
  got: number;
}

/**
 * 续传客户端。
 *
 * 校验与分类：
 * - 每条事件消息携带 prevSeq，与上一条投递形成链；chainSeq 为期望的
 *   下一个 prevSeq，由 snapshot.atSeq 或 resumed.fromSeq 建立基线，
 *   链断裂记为缺口（切换处遗漏可被发现）；
 * - 事件 seq <= lastSeq 为可明确识别的重复（重连时游标落后于
 *   已应用位置所致）；
 * - 游标按 checkpointEvery 条消息持久化一次，模拟崩溃后只能从
 *   较早的检查点恢复。
 */
export class ResumeClient {
  readonly filter: Filter;
  lastSeq = 0;
  /** 已持久化的游标，重连时出示。 */
  persistedCursor: string | undefined;
  readonly state = new Map<string, StateEntry>();
  readonly applied: EventRecord[] = [];
  readonly duplicates: EventRecord[] = [];
  readonly gaps: Gap[] = [];
  private chainSeq = 0;
  private sinceCheckpoint = 0;
  private readonly checkpointEvery: number;

  constructor(filter: Filter, checkpointEvery = 1) {
    this.filter = filter;
    this.checkpointEvery = checkpointEvery;
  }

  /** 消费 count 条服务端消息（不含 live 标记）后断开。 */
  async consume(server: EventServer, count: number): Promise<void> {
    const stream = server.subscribe(this.filter, this.persistedCursor);
    let received = 0;
    for await (const message of stream) {
      this.handle(message);
      if (message.type === 'live' || message.type === 'resumed') continue;
      received += 1;
      if (received >= count) break;
    }
  }

  /** 消费到追平实时流（收到 live 标记）后断开。 */
  async consumeUntilLive(server: EventServer): Promise<void> {
    const stream = server.subscribe(this.filter, this.persistedCursor);
    for await (const message of stream) {
      this.handle(message);
      if (message.type === 'live') break;
    }
  }

  private handle(message: ServerMessage): void {
    switch (message.type) {
      case 'live':
        return;
      case 'resumed':
        this.chainSeq = message.fromSeq;
        return;
      case 'snapshot':
        this.state.clear();
        for (const entry of message.entries) {
          this.state.set(stateKey(entry.topic, entry.key), entry);
        }
        this.lastSeq = message.atSeq;
        this.chainSeq = message.atSeq;
        this.persistedCursor = message.cursor;
        this.sinceCheckpoint = 0;
        return;
    }
    const { event } = message;
    if (message.prevSeq !== this.chainSeq) {
      this.gaps.push({ expected: this.chainSeq, got: message.prevSeq });
    }
    this.chainSeq = event.seq;
    if (event.seq <= this.lastSeq) {
      this.duplicates.push(event);
    } else {
      this.state.set(stateKey(event.topic, event.key), {
        topic: event.topic,
        key: event.key,
        value: event.value,
      });
      this.applied.push(event);
      this.lastSeq = event.seq;
    }
    this.sinceCheckpoint += 1;
    if (this.sinceCheckpoint >= this.checkpointEvery) {
      this.persistedCursor = message.cursor;
      this.sinceCheckpoint = 0;
    }
  }
}
