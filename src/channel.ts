export class ChannelClosedError extends Error {
  constructor(channelName: string) {
    super(`channel "${channelName}" is closed`);
    this.name = 'ChannelClosedError';
  }
}

export class ChannelFailedError extends Error {
  readonly reason: unknown;
  constructor(channelName: string, reason: unknown) {
    super(
      `channel "${channelName}" failed: ${
        reason instanceof Error ? reason.message : String(reason)
      }`,
    );
    this.name = 'ChannelFailedError';
    this.reason = reason;
  }
}

export interface RecvResult<T> {
  done: boolean;
  item?: T;
}

interface SendWaiter<T> {
  item: T;
  resolve: () => void;
  reject: (err: Error) => void;
}

interface RecvWaiter<T> {
  resolve: (result: RecvResult<T>) => void;
  reject: (err: Error) => void;
}

type ChannelState = 'open' | 'closed' | 'failed';

/**
 * A bounded async channel.
 *
 * Backpressure: `send` blocks while the buffer is full, so a paused
 * receiver progressively stops senders (and therefore upstream pulls).
 *
 * Ownership: ownership of an item transfers to the receiving side only
 * when `send` completes. If `send` rejects, the sender still owns the
 * item. Items left buffered when the channel fails stay owned by the
 * receiving stage, which reclaims them via `drain()`.
 */
export class Channel<T> {
  private buffer: T[] = [];
  private sendWaiters: Array<SendWaiter<T>> = [];
  private recvWaiters: Array<RecvWaiter<T>> = [];
  private state: ChannelState = 'open';
  private failure: unknown = null;

  readonly capacity: number;
  readonly name: string;

  constructor(capacity: number, name = 'channel') {
    if (!Number.isInteger(capacity) || capacity < 1) {
      throw new RangeError('channel capacity must be a positive integer');
    }
    this.capacity = capacity;
    this.name = name;
  }

  get bufferedCount(): number {
    return this.buffer.length;
  }

  get pendingSends(): number {
    return this.sendWaiters.length;
  }

  get isOpen(): boolean {
    return this.state === 'open';
  }

  async send(item: T): Promise<void> {
    if (this.state === 'failed') throw new ChannelFailedError(this.name, this.failure);
    if (this.state === 'closed') throw new ChannelClosedError(this.name);

    const recvWaiter = this.recvWaiters.shift();
    if (recvWaiter) {
      recvWaiter.resolve({ done: false, item });
      return;
    }
    if (this.buffer.length < this.capacity) {
      this.buffer.push(item);
      return;
    }
    return new Promise<void>((resolve, reject) => {
      this.sendWaiters.push({ item, resolve, reject });
    });
  }

  async recv(): Promise<RecvResult<T>> {
    if (this.state === 'failed') throw new ChannelFailedError(this.name, this.failure);

    const item = this.buffer.shift();
    if (item !== undefined) {
      this.promoteSendWaiters();
      return { done: false, item };
    }
    if (this.state === 'closed') return { done: true };

    return new Promise<RecvResult<T>>((resolve, reject) => {
      this.recvWaiters.push({ resolve, reject });
    });
  }

  /** Graceful close: no more sends; buffered items remain receivable. */
  close(): void {
    if (this.state !== 'open') return;
    this.state = 'closed';
    const senders = this.sendWaiters.splice(0);
    for (const waiter of senders) waiter.reject(new ChannelClosedError(this.name));
    const receivers = this.recvWaiters.splice(0);
    for (const waiter of receivers) waiter.resolve({ done: true });
  }

  /**
   * Terminal failure: pending/future sends and recvs throw. Buffered
   * items are NOT dropped; the receiving stage reclaims them via drain().
   */
  fail(reason: unknown): void {
    if (this.state === 'failed') return;
    this.state = 'failed';
    this.failure = reason;
    const senders = this.sendWaiters.splice(0);
    for (const waiter of senders) {
      waiter.reject(new ChannelFailedError(this.name, reason));
    }
    const receivers = this.recvWaiters.splice(0);
    for (const waiter of receivers) {
      waiter.reject(new ChannelFailedError(this.name, reason));
    }
  }

  /** Take buffered items; ownership passes to the caller (the receiver). */
  drain(): T[] {
    return this.buffer.splice(0);
  }

  private promoteSendWaiters(): void {
    while (this.sendWaiters.length > 0) {
      const recvWaiter = this.recvWaiters.shift();
      const next = this.sendWaiters[0];
      if (recvWaiter) {
        this.sendWaiters.shift();
        recvWaiter.resolve({ done: false, item: next.item });
        next.resolve();
      } else if (this.buffer.length < this.capacity) {
        this.sendWaiters.shift();
        this.buffer.push(next.item);
        next.resolve();
      } else {
        break;
      }
    }
  }
}
