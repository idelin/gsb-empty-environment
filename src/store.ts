import { VectorClock, type ClockData } from './vectorClock.ts';

/** 一个版本：值 + 标记它的向量时钟 + 产生它的副本。 */
export interface Version<V> {
  clock: VectorClock;
  value: V;
  origin: string;
}

/** 传播事件。put/resolve 都携带新版本的完整时钟，远端据此做幂等归并。 */
export type StoreEvent<V> =
  | { kind: 'put'; id: string; key: string; origin: string; clock: ClockData; value: V }
  | {
      kind: 'resolve';
      id: string;
      key: string;
      origin: string;
      clock: ClockData;
      /** 调用者解决时所见版本的上下文时钟（冗余于 clock，用于审计/校验）。 */
      context: ClockData;
      value: V;
    };

export interface GetResult<V> {
  /** 当前键下所有互相并发的版本。 */
  versions: Version<V>[];
  /** 所见版本的归并时钟；解决冲突时必须原样回传。 */
  context: VectorClock;
}

/**
 * 单副本的版本化键值存储。
 *
 * 不变式：
 * - 同一键下保留的版本两两并发；被支配的版本立即回收。
 * - resolve 的新时钟 = 调用者上下文 + 本副本自增一次，因此恰好支配
 *   “调用者见过的全部版本”；与上下文并发的版本（调用者未见）绝不删除。
 * - applyEvent 幂等且与顺序无关：靠时钟支配关系去重、回收。
 */
export class VersionedStore<V> {
  readonly replicaId: string;
  /** 本副本已见全部事件的上界时钟。 */
  private clock = VectorClock.empty();
  private readonly data = new Map<string, Version<V>[]>();
  /** 本地产生的事件日志，用于向其他副本传播。 */
  readonly log: StoreEvent<V>[] = [];
  private seq = 0;

  constructor(replicaId: string) {
    this.replicaId = replicaId;
  }

  get(key: string): GetResult<V> {
    const stored = this.data.get(key) ?? [];
    const context = VectorClock.empty();
    const versions = stored.map((v) => {
      context.merge(v.clock);
      return { clock: v.clock.clone(), value: v.value, origin: v.origin };
    });
    return { versions, context };
  }

  /** 盲写：新版本支配本副本已见的该键全部旧版本。 */
  put(key: string, value: V): StoreEvent<V> {
    this.clock.increment(this.replicaId);
    const clock = this.clock.clone();
    const event: StoreEvent<V> = {
      kind: 'put',
      id: this.nextId(),
      key,
      origin: this.replicaId,
      clock: clock.toData(),
      value,
    };
    this.integrate(key, { clock, value, origin: this.replicaId });
    this.log.push(event);
    return event;
  }

  /**
   * 显式解决：value 为调用者给出的解决值，context 必须来自此前的 get。
   * 新时钟只覆盖 context，因此未出现在 context 中的版本（调用者未见）
   * 在本地和事件传播到的所有副本上都会被保留。
   */
  resolve(key: string, value: V, context: VectorClock): StoreEvent<V> {
    const clock = context.clone();
    const tick = Math.max(clock.get(this.replicaId), this.clock.get(this.replicaId)) + 1;
    clock.set(this.replicaId, tick);
    this.clock.merge(clock);
    const event: StoreEvent<V> = {
      kind: 'resolve',
      id: this.nextId(),
      key,
      origin: this.replicaId,
      clock: clock.toData(),
      context: context.toData(),
      value,
    };
    this.integrate(key, { clock, value, origin: this.replicaId });
    this.log.push(event);
    return event;
  }

  /** 应用远端事件。重复、乱序、交叉传播均安全。 */
  applyEvent(event: StoreEvent<V>): void {
    const clock = VectorClock.fromData(event.clock);
    this.clock.merge(clock);
    this.integrate(event.key, { clock, value: event.value, origin: event.origin });
  }

  /** 归并一个版本：丢弃被支配者，保留并发者，忽略已支配的重复/过期事件。 */
  private integrate(key: string, incoming: Version<V>): void {
    const versions = this.data.get(key) ?? [];
    for (const v of versions) {
      if (v.clock.dominates(incoming.clock)) return;
    }
    const kept = versions.filter((v) => !incoming.clock.dominates(v.clock));
    kept.push(incoming);
    this.data.set(key, kept);
  }

  /** 状态指纹：用于跨副本一致性断言。 */
  fingerprint(): string {
    const out: Record<string, { clock: ClockData; value: V }[]> = {};
    for (const key of [...this.data.keys()].sort()) {
      out[key] = this.data
        .get(key)!
        .map((v) => ({ clock: v.clock.toData(), value: v.value }))
        .sort((a, b) => JSON.stringify(a.clock).localeCompare(JSON.stringify(b.clock)));
    }
    return JSON.stringify(out);
  }

  private nextId(): string {
    this.seq += 1;
    return this.replicaId + '#' + this.seq;
  }
}
