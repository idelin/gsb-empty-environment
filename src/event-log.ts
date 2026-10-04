import {
  matchesFilter,
  stateKey,
  type EventRecord,
  type Filter,
  type StateEntry,
} from './types.ts';

export type AppendListener = (event: EventRecord) => void;

/**
 * 仅追加的事件日志，支持压缩：
 * compact(beforeSeq) 将 seq < beforeSeq 的事件折叠进基准状态后丢弃，
 * 之后只能恢复 seq >= compactedBefore 的游标。
 */
export class EventLog {
  private retained: EventRecord[] = [];
  private baseState = new Map<string, StateEntry>();
  private latestSeq = 0;
  private compactedBeforeSeq = 0;
  private listeners = new Set<AppendListener>();

  append(topic: string, key: string, value: unknown): EventRecord {
    const event: EventRecord = {
      seq: ++this.latestSeq,
      topic,
      key,
      value,
      ts: Date.now(),
    };
    this.retained.push(event);
    for (const listener of this.listeners) listener(event);
    return event;
  }

  onAppend(listener: AppendListener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  getLatestSeq(): number {
    return this.latestSeq;
  }

  /** 压缩水位线：seq 小于它的事件已被丢弃，折叠进基准状态。 */
  getCompactedBefore(): number {
    return this.compactedBeforeSeq;
  }

  compact(beforeSeq: number): void {
    const cutoff = Math.min(beforeSeq, this.latestSeq + 1);
    if (cutoff <= this.compactedBeforeSeq) return;
    const keep: EventRecord[] = [];
    for (const event of this.retained) {
      if (event.seq < cutoff) {
        this.applyTo(this.baseState, event);
      } else {
        keep.push(event);
      }
    }
    this.retained = keep;
    this.compactedBeforeSeq = cutoff;
  }

  /** seq 之后（不含）且匹配订阅条件的保留事件，按 seq 升序。 */
  eventsAfter(seq: number, filter: Filter): EventRecord[] {
    return this.retained.filter(
      (event) => event.seq > seq && matchesFilter(event, filter),
    );
  }

  /** 截至 seq（含）的状态，要求 seq >= 压缩水位线。 */
  stateAt(seq: number): Map<string, StateEntry> {
    if (seq < this.compactedBeforeSeq) {
      throw new Error(
        `state at seq ${seq} is no longer available (compacted before ${this.compactedBeforeSeq})`,
      );
    }
    const state = new Map(this.baseState);
    for (const event of this.retained) {
      if (event.seq > seq) break;
      this.applyTo(state, event);
    }
    return state;
  }

  private applyTo(state: Map<string, StateEntry>, event: EventRecord): void {
    state.set(stateKey(event.topic, event.key), {
      topic: event.topic,
      key: event.key,
      value: event.value,
    });
  }
}
