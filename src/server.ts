import {
  CursorError,
  encodeCursor,
  validateCursorForFilter,
} from './cursor.ts';
import { EventLog } from './event-log.ts';
import {
  matchesFilter,
  type EventRecord,
  type Filter,
  type StateEntry,
} from './types.ts';

/**
 * 服务端消息流：
 * - snapshot：全量状态 + 增量起点 atSeq（新订阅或游标过旧时）；
 * - resumed：从有效游标续传，fromSeq 为重放起点；
 * - event：携带 prevSeq，与本订阅流上一条投递的消息形成链，
 *   客户端据此验证切换处无遗漏（被过滤的事件不在链上）；
 * - live：已追平，之后为实时事件。
 */
export type ServerMessage =
  | { type: 'snapshot'; entries: StateEntry[]; atSeq: number; cursor: string }
  | { type: 'resumed'; fromSeq: number; cursor: string }
  | { type: 'event'; event: EventRecord; prevSeq: number; cursor: string }
  | { type: 'live' };

export interface EventServerOptions {
  /** 模拟快照生成耗时，期间事件继续写入日志。 */
  snapshotDelayMs?: number;
  /** 快照生成期间的钩子，测试/模拟器用它确定性地注入并发事件。 */
  onSnapshotBuild?: (atSeq: number) => void | Promise<void>;
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/** 无界缓冲的异步队列：实时事件先入队，消费端按序取出。 */
class EventQueue {
  private items: EventRecord[] = [];
  private waiters: ((event: EventRecord) => void)[] = [];

  push(event: EventRecord): void {
    const waiter = this.waiters.shift();
    if (waiter) waiter(event);
    else this.items.push(event);
  }

  next(): Promise<EventRecord> {
    const item = this.items.shift();
    if (item !== undefined) return Promise.resolve(item);
    return new Promise((resolve) => this.waiters.push(resolve));
  }
}

/**
 * 续传协议服务端。
 *
 * 恢复决策：
 * - 无游标             → 快照 + 增量
 * - 游标过旧（被压缩） → 快照 + 增量（CURSOR_TOO_OLD 的恢复路径）
 * - 游标有效           → resumed + 精确重放
 * - 游标跨订阅条件     → CURSOR_FILTER_MISMATCH，拒绝
 *
 * 切换无遗漏的保证：先注册实时监听（事件入队缓冲），再读日志重放，
 * 最后排空缓冲并转入实时流；日志是唯一事实来源，两段覆盖区间有重叠、
 * 按 seq 去重，因此交界处既不丢也不乱。
 */
export class EventServer {
  private readonly log: EventLog;
  private readonly snapshotDelayMs: number;
  private readonly onSnapshotBuild?: (atSeq: number) => void | Promise<void>;

  constructor(log: EventLog, options: EventServerOptions = {}) {
    this.log = log;
    this.snapshotDelayMs = options.snapshotDelayMs ?? 0;
    this.onSnapshotBuild = options.onSnapshotBuild;
  }

  async *subscribe(
    filter: Filter,
    cursorToken?: string,
  ): AsyncGenerator<ServerMessage> {
    let fromSeq: number;
    if (cursorToken === undefined) {
      fromSeq = yield* this.emitSnapshot(filter);
    } else {
      try {
        fromSeq = validateCursorForFilter(
          cursorToken,
          filter,
          this.log.getCompactedBefore(),
        );
        // 游标有效：显式告知续传起点，建立客户端校验链的基线。
        yield { type: 'resumed', fromSeq, cursor: cursorToken };
      } catch (error) {
        if (error instanceof CursorError && error.code === 'CURSOR_TOO_OLD') {
          fromSeq = yield* this.emitSnapshot(filter);
        } else {
          throw error;
        }
      }
    }
    yield* this.replayThenLive(filter, fromSeq);
  }

  /** 生成快照：先记下起点 seq，快照期间事件继续产生，由后续增量覆盖。 */
  private async *emitSnapshot(
    filter: Filter,
  ): AsyncGenerator<ServerMessage, number> {
    const atSeq = this.log.getLatestSeq();
    if (this.snapshotDelayMs > 0) await sleep(this.snapshotDelayMs);
    await this.onSnapshotBuild?.(atSeq);
    const state = this.log.stateAt(atSeq);
    const entries = [...state.values()].filter((entry) =>
      matchesFilter(entry, filter),
    );
    yield {
      type: 'snapshot',
      entries,
      atSeq,
      cursor: encodeCursor(filter, atSeq),
    };
    return atSeq;
  }

  private async *replayThenLive(
    filter: Filter,
    fromSeq: number,
  ): AsyncGenerator<ServerMessage> {
    // 先挂监听，再读日志：两者间到达的事件进入队列，不会丢失。
    const queue = new EventQueue();
    const detach = this.log.onAppend((event) => queue.push(event));
    try {
      let lastDelivered = fromSeq;
      for (const event of this.log.eventsAfter(lastDelivered, filter)) {
        yield {
          type: 'event',
          event,
          prevSeq: lastDelivered,
          cursor: encodeCursor(filter, event.seq),
        };
        lastDelivered = event.seq;
      }
      yield { type: 'live' };
      for (;;) {
        const event = await queue.next();
        if (event.seq <= lastDelivered) continue; // 重放段已覆盖，跳过重叠
        if (!matchesFilter(event, filter)) continue;
        yield {
          type: 'event',
          event,
          prevSeq: lastDelivered,
          cursor: encodeCursor(filter, event.seq),
        };
        lastDelivered = event.seq;
      }
    } finally {
      detach();
    }
  }
}
