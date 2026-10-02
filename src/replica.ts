import {
  emptyState,
  applyEvent,
  reconcile,
  versionGraphSize,
  type DottedEvent,
  type KeyState,
} from './dvv.ts';
import { mergeDot, type Dot, type VersionVector } from './version-vector.ts';

export interface ReadResult<V> {
  values: V[];
  dots: Dot[];
  context: VersionVector;
  conflict: boolean;
}

/** 无主（Leaderless）复制节点：本地写 + 点状事件广播 + 反熵同步。 */
export class Replica<V = unknown> {
  readonly id: string;
  private readonly keys = new Map<string, KeyState<V>>();

  constructor(id: string) {
    this.id = id;
  }

  state(key: string): KeyState<V> {
    return this.keys.get(key) ?? emptyState<V>();
  }

  /**
   * 本地写入：分配新的点状版本标识，前置向量折叠所有已见兄弟分支
   * （一次写覆盖写入者已观察到的全部并发值），返回待广播的点状事件。
   */
  write(key: string, value: V): DottedEvent<V> {
    const state = this.state(key);
    let counter = (state.context[this.id] ?? 0) + 1;
    for (const { dot } of state.values) {
      if (dot.node === this.id && dot.counter >= counter) counter = dot.counter + 1;
    }
    const dot: Dot = { node: this.id, counter };
    let context = state.context;
    for (const entry of state.values) context = mergeDot(context, entry.dot);
    const event: DottedEvent<V> = { kind: 'dvv-event', key, dot, context, value };
    this.keys.set(key, { values: [{ dot, value }], context });
    return event;
  }

  /** 接收（可能乱序/重复的）广播事件。 */
  receive(event: DottedEvent<V>): void {
    this.keys.set(event.key, applyEvent(this.state(event.key), event));
  }

  /** 与另一副本做双向反熵同步（全量单键状态合并）。 */
  sync(key: string, other: Replica<V>): void {
    const merged = reconcile(this.state(key), other.state(key));
    this.keys.set(key, merged);
    other.keys.set(key, merged);
  }

  read(key: string): ReadResult<V> {
    const state = this.state(key);
    return {
      values: state.values.map((entry) => entry.value),
      dots: state.values.map((entry) => entry.dot),
      context: state.context,
      conflict: state.values.length > 1,
    };
  }

  graphSize(key: string): number {
    return versionGraphSize(this.state(key));
  }
}
