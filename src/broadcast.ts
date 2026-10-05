/**
 * 广播订阅库：每个订阅者拥有固定容量的消息缓冲。
 *
 * 语义约定：
 * - 每条消息拥有全局单调递增的序号（从 1 开始）。
 * - 订阅者缓冲溢出时，最旧的消息被丢弃；交付前先发一个包含丢失序号范围
 *   [lostFrom, lostTo] 的缺口事件，然后继续交付缓冲中的最新数据。
 * - publish 采用快照语义：本轮发布期间新订阅的监听者【不接收】当前事件，
 *   从下一次 publish 开始接收。
 * - 退订立即生效：缓冲中未交付的事件被丢弃，监听者此后不再被调用；
 *   对已退订的订阅再次调用 unsubscribe() 会抛出 UnsubscribedError。
 * - 监听者可以在回调中同步退订、订阅、再次发布（重入）。
 * - 监听者抛错不影响其他监听者的交付；publish 在完成全部交付后，
 *   以 AggregateError 汇总抛出本轮所有监听者异常。
 */

/** 数据事件 */
export interface DataEvent<T> {
  readonly kind: 'data';
  /** 全局单调递增序号，从 1 开始 */
  readonly seq: number;
  readonly value: T;
}

/** 缺口事件：序号 [lostFrom, lostTo] 范围内的消息因缓冲溢出被丢弃 */
export interface GapEvent {
  readonly kind: 'gap';
  readonly lostFrom: number;
  readonly lostTo: number;
}

export type BroadcastEvent<T> = DataEvent<T> | GapEvent;

export type Listener<T> = (event: BroadcastEvent<T>) => void;

export interface SubscribeOptions {
  /** 每个订阅者的缓冲容量（消息条数），必须 >= 1，默认 16 */
  readonly capacity?: number;
}

/** 对已退订的订阅再次调用 unsubscribe() 时抛出 */
export class UnsubscribedError extends Error {
  constructor(message = 'subscription has already been unsubscribed') {
    super(message);
    this.name = 'UnsubscribedError';
  }
}

const DEFAULT_CAPACITY = 16;

export class Subscription<T> {
  private buffer: Array<DataEvent<T>> = [];
  private gapFrom = 0; // 待交付缺口起点，0 表示无待交付缺口
  private gapTo = 0;   // 待交付缺口终点
  private delivering = false;
  private closed = false;

  /** @internal */
  constructor(
    private readonly broadcast: Broadcast<T>,
    private readonly listener: Listener<T>,
    readonly capacity: number,
  ) {}

  /** 是否仍然有效（未退订） */
  get active(): boolean {
    return !this.closed;
  }

  /**
   * 退订。立即生效：丢弃缓冲中未交付的事件，监听者不再被调用。
   * 重复调用抛出 UnsubscribedError。
   */
  unsubscribe(): void {
    if (this.closed) {
      throw new UnsubscribedError();
    }
    this.closed = true;
    this.buffer.length = 0;
    this.gapFrom = 0;
    this.gapTo = 0;
    this.broadcast.remove(this);
  }

  /** @internal 入队一条消息；溢出时丢弃最旧消息并记录丢失序号范围 */
  enqueue(event: DataEvent<T>): void {
    if (this.closed) return;
    if (this.buffer.length === this.capacity) {
      const dropped = this.buffer.shift()!;
      if (this.gapTo === 0) {
        this.gapFrom = dropped.seq;
        this.gapTo = dropped.seq;
      } else {
        // 序号连续，丢失范围只会向后延伸
        this.gapTo = dropped.seq;
      }
    }
    this.buffer.push(event);
  }

  /** @internal 交付缓冲中的事件；重入时直接返回，由外层循环继续交付 */
  pump(errors: unknown[]): void {
    if (this.closed || this.delivering) return;
    this.delivering = true;
    try {
      for (;;) {
        if (this.closed) return;
        let event: BroadcastEvent<T>;
        if (this.gapTo !== 0) {
          event = { kind: 'gap', lostFrom: this.gapFrom, lostTo: this.gapTo };
          this.gapFrom = 0;
          this.gapTo = 0;
        } else if (this.buffer.length > 0) {
          event = this.buffer.shift()!;
        } else {
          return;
        }
        try {
          this.listener(event);
        } catch (err) {
          errors.push(err);
        }
      }
    } finally {
      this.delivering = false;
    }
  }
}

export class Broadcast<T> {
  private seq = 0;
  private readonly subs = new Set<Subscription<T>>();

  /** 当前活跃订阅数 */
  get size(): number {
    return this.subs.size;
  }

  /** 订阅广播，返回订阅句柄 */
  subscribe(listener: Listener<T>, options: SubscribeOptions = {}): Subscription<T> {
    const capacity = options.capacity ?? DEFAULT_CAPACITY;
    if (!Number.isInteger(capacity) || capacity < 1) {
      throw new RangeError(`capacity must be an integer >= 1, got ${capacity}`);
    }
    const sub = new Subscription<T>(this, listener, capacity);
    this.subs.add(sub);
    return sub;
  }

  /**
   * 发布一条消息，同步交付给本轮快照中的所有活跃订阅者。
   * 本轮新订阅者不接收当前事件；监听者抛错被收集，
   * 全部交付完成后以 AggregateError 抛出。
   */
  publish(value: T): void {
    this.seq += 1;
    const event: DataEvent<T> = { kind: 'data', seq: this.seq, value };
    const errors: unknown[] = [];
    const snapshot = Array.from(this.subs);
    // 先入队、后交付：保证重入发布时每个订阅者仍按序号顺序接收事件
    for (const sub of snapshot) {
      sub.enqueue(event);
    }
    for (const sub of snapshot) {
      sub.pump(errors);
    }
    if (errors.length > 0) {
      throw new AggregateError(errors, `${errors.length} listener(s) threw during publish`);
    }
  }

  /** @internal */
  remove(sub: Subscription<T>): void {
    this.subs.delete(sub);
  }
}
