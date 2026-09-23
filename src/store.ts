/**
 * 事件存储抽象。内存实现之外，可以换成写文件/数据库的实现：
 * 启动时把历史事件全部取出交给 Coordinator 重放即可恢复状态。
 */
import type { GroupEvent } from "./events.ts";

export interface EventStore<T> {
  append(event: GroupEvent<T>): void;
  load(): readonly GroupEvent<T>[];
}

export class MemoryEventStore<T> implements EventStore<T> {
  private readonly events: GroupEvent<T>[] = [];

  constructor(seed?: readonly GroupEvent<T>[]) {
    if (seed) this.events.push(...seed);
  }

  append(event: GroupEvent<T>): void {
    this.events.push(event);
  }

  load(): readonly GroupEvent<T>[] {
    return [...this.events];
  }
}
