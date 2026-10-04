/**
 * 广播订阅库：每个订阅者拥有固定容量的消息缓冲。
 *
 * 语义约定：
 * - 每条被发布的事件获得一个全局递增的序号 seq（从 1 开始）。
 * - 每个订阅者拥有容量为 capacity 的缓冲区；溢出时丢弃最旧的消息，
 *   并记录丢失序号的连续区间。下一次向该订阅者交付时，先交付一个
 *   gap 事件 { kind: 'gap', from, to }（闭区间），然后继续交付缓冲中
 *   的最新数据。
 * - 退订立即生效且幂等：退订后监听器不会再被调用（包括当前这一轮
 *   发布中尚未交付的部分）。
 * - 发布过程中监听器可以同步退订、订阅新监听器、或抛错：
 *   - 本轮新订阅的监听器【不会】收到当前正在发布的事件，
 *     从下一次 publish 开始接收。
 *   - 监听器抛错不会中断其他订阅者的交付；本轮所有交付完成后，
 *     publish 抛出 AggregateError 汇总本轮所有监听器异常。
 */

export interface DataEvent<T> {
  readonly kind: 'data';
  /** 全局递增序号，从 1 开始 */
  readonly seq: number;
  readonly value: T;
}

export interface GapEvent {
  readonly kind: 'gap';
  /** 丢失序号区间起点（含） */
  readonly from: number;
  /** 丢失序号区间终点（含） */
  readonly to: number;
}

export type BroadcastEvent<T> = DataEvent<T> | GapEvent;

export type Listener<T> = (event: BroadcastEvent<T>) => void;

export interface Subscription {
  /** 退订。幂等；调用后监听器保证不再被调用。 */
  unsubscribe(): void;
  /** 是否已退订 */
  readonly closed: boolean;
}

export interface BroadcastOptions {
  /** 每个订阅者的缓冲容量，必须为正整数，默认 16 */
  capacity?: number;
}

interface BufferedMessage<T> {
  seq: number;
  value: T;
}

interface SubscriberState<T> {
  listener: Listener<T>;
  queue: BufferedMessage<T>[];
  /** 待交付的丢失区间（闭区间），gapFrom 为 null 表示无丢失 */
  gapFrom: number | null;
  gapTo: number;
  active: boolean;
  delivering: boolean;
}

export class Broadcast<T> {
  readonly #capacity: number;
  #seq = 0;
  readonly #subscribers = new Set<SubscriberState<T>>();

  constructor(options: BroadcastOptions = {}) {
    const capacity = options.capacity ?? 16;
    if (!Number.isInteger(capacity) || capacity < 1) {
      throw new RangeError(`capacity 必须为正整数，收到: ${capacity}`);
    }
    this.#capacity = capacity;
  }

  /** 当前活跃订阅者数量 */
  get subscriberCount(): number {
    return this.#subscribers.size;
  }

  subscribe(listener: Listener<T>): Subscription {
    const state: SubscriberState<T> = {
      listener,
      queue: [],
      gapFrom: null,
      gapTo: 0,
      active: true,
      delivering: false,
    };
    this.#subscribers.add(state);

    const subscription: Subscription = {
      unsubscribe: () => {
        if (!state.active) return;
        state.active = false;
        state.queue.length = 0;
        this.#subscribers.delete(state);
      },
      get closed() {
        return !state.active;
      },
    };
    return subscription;
  }

  /**
   * 发布一条消息：先写入所有当前订阅者的缓冲区，再逐订阅者同步交付。
   *
   * 本轮新订阅的监听器不会收到本次事件。
   * 监听器抛错被收集，全部交付完成后以 AggregateError 抛出。
   */
  publish(value: T): void {
    const seq = ++this.#seq;
    // 快照：发布期间新订阅者不参与本轮
    const targets = [...this.#subscribers];
    for (const sub of targets) {
      this.#enqueue(sub, { seq, value });
    }

    const errors: unknown[] = [];
    for (const sub of targets) {
      this.#drain(sub, errors);
    }
    if (errors.length > 0) {
      throw new AggregateError(
        errors,
        `publish(seq=${seq}) 期间有 ${errors.length} 次监听器调用抛错`,
      );
    }
  }

  #enqueue(sub: SubscriberState<T>, msg: BufferedMessage<T>): void {
    if (sub.queue.length === this.#capacity) {
      const dropped = sub.queue.shift()!;
      if (sub.gapFrom === null) {
        sub.gapFrom = dropped.seq;
      }
      // 序号全局递增且按序丢弃，丢失区间始终连续
      sub.gapTo = dropped.seq;
    }
    sub.queue.push(msg);
  }

  #drain(sub: SubscriberState<T>, errors: unknown[]): void {
    if (!sub.active || sub.delivering) return;
    sub.delivering = true;
    try {
      while (sub.active) {
        let event: BroadcastEvent<T>;
        if (sub.gapFrom !== null) {
          event = { kind: 'gap', from: sub.gapFrom, to: sub.gapTo };
          sub.gapFrom = null;
        } else if (sub.queue.length > 0) {
          const msg = sub.queue.shift()!;
          event = { kind: 'data', seq: msg.seq, value: msg.value };
        } else {
          break;
        }
        try {
          sub.listener(event);
        } catch (err) {
          errors.push(err);
        }
      }
    } finally {
      sub.delivering = false;
    }
  }
}
