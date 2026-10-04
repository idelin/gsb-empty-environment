/**
 * Bounded asynchronous channel.
 *
 * `send()` resolves only once the item is buffered or handed to a receiver,
 * so a full channel applies backpressure to the producer. `receive()`
 * resolves with `undefined` once the channel is closed and drained, and
 * rejects with the closing error when closed with a failure.
 */

export class ChannelClosedError extends Error {
  readonly channelName: string;

  constructor(channelName: string) {
    super(`channel "${channelName}" is closed`);
    this.name = "ChannelClosedError";
    this.channelName = channelName;
  }
}

interface SendWaiter<T> {
  readonly item: T;
  readonly resolve: () => void;
  readonly reject: (err: unknown) => void;
}

interface RecvWaiter<T> {
  readonly resolve: (value: T | undefined) => void;
  readonly reject: (err: unknown) => void;
}

export class BoundedChannel<T> {
  readonly capacity: number;
  readonly name: string;

  private readonly buffer: T[] = [];
  private readonly sendWaiters: Array<SendWaiter<T>> = [];
  private readonly recvWaiters: Array<RecvWaiter<T>> = [];
  private closedFlag = false;
  private closeError: unknown = undefined;

  constructor(capacity: number, name = "channel") {
    if (!Number.isInteger(capacity) || capacity < 1) {
      throw new RangeError(`capacity must be a positive integer, got ${capacity}`);
    }
    this.capacity = capacity;
    this.name = name;
  }

  get isClosed(): boolean {
    return this.closedFlag;
  }

  get size(): number {
    return this.buffer.length;
  }

  send(item: T): Promise<void> {
    if (this.closedFlag) {
      return Promise.reject(new ChannelClosedError(this.name));
    }
    const recv = this.recvWaiters.shift();
    if (recv) {
      recv.resolve(item);
      return Promise.resolve();
    }
    if (this.buffer.length < this.capacity) {
      this.buffer.push(item);
      return Promise.resolve();
    }
    return new Promise<void>((resolve, reject) => {
      this.sendWaiters.push({ item, resolve, reject });
    });
  }

  receive(): Promise<T | undefined> {
    if (this.buffer.length > 0) {
      const item = this.buffer.shift() as T;
      this.pump();
      return Promise.resolve(item);
    }
    if (this.closedFlag) {
      if (this.closeError !== undefined) return Promise.reject(this.closeError);
      return Promise.resolve(undefined);
    }
    return new Promise<T | undefined>((resolve, reject) => {
      this.recvWaiters.push({ resolve, reject });
    });
  }

  close(error?: unknown): void {
    if (this.closedFlag) return;
    this.closedFlag = true;
    this.closeError = error;
    const waiters = this.sendWaiters.splice(0);
    for (const w of waiters) w.reject(new ChannelClosedError(this.name));
    this.pump();
  }

  /** Remove and return everything still buffered (ownership passes to the caller). */
  drain(): T[] {
    const items = this.buffer.splice(0);
    this.pump();
    return items;
  }

  private pump(): void {
    while (this.recvWaiters.length > 0 && this.buffer.length > 0) {
      (this.recvWaiters.shift() as RecvWaiter<T>).resolve(this.buffer.shift() as T);
    }
    while (
      this.sendWaiters.length > 0 &&
      !this.closedFlag &&
      this.buffer.length < this.capacity
    ) {
      const waiter = this.sendWaiters.shift() as SendWaiter<T>;
      const recv = this.recvWaiters.shift();
      if (recv) recv.resolve(waiter.item);
      else this.buffer.push(waiter.item);
      waiter.resolve();
    }
    if (this.closedFlag && this.buffer.length === 0) {
      const waiters = this.recvWaiters.splice(0);
      for (const w of waiters) {
        if (this.closeError !== undefined) w.reject(this.closeError);
        else w.resolve(undefined);
      }
    }
  }
}
