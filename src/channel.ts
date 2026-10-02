import { RingBuffer } from "./ring-buffer.ts";

/** 接收结果，对应 Go 的 `v, ok := <-ch`：done=true 表示通道已关闭且缓冲区耗尽（EOF）。 */
export type RecvResult<T> =
  | { value: T; done: false }
  | { value: undefined; done: true };

interface WaitingSender<T> {
  value: T;
  resolve: () => void;
}

interface WaitingReceiver<T> {
  resolve: (result: RecvResult<T>) => void;
}

/**
 * 仿 Go 语义的协程 Channel。
 *
 * - 支持带缓冲（capacity > 0）与无缓冲（capacity = 0）两种模式；
 * - 缓冲区满（或无缓冲且无等待接收者）时，send 挂起当前协程；
 *   缓冲区空（且无等待发送者）时，recv 挂起当前协程；
 * - 对端就绪时按 FIFO 精确唤醒匹配的等待协程（一对一交接，不广播数据）；
 * - close 后：缓冲区剩余数据仍可被消费，随后所有 recv 收到 EOF；
 *   所有等待中的接收协程被立即广播唤醒并收到 EOF；
 * - 向已关闭的 Channel 发送会抛错（同 Go 的 panic 语义）。
 */
export class Channel<T> {
  private readonly buffer: RingBuffer<T>;
  private readonly sendQueue: WaitingSender<T>[] = [];
  private readonly recvQueue: WaitingReceiver<T>[] = [];
  private closedFlag = false;
  readonly capacity: number;

  constructor(capacity = 0) {
    this.capacity = capacity;
    this.buffer = new RingBuffer<T>(capacity);
  }

  get closed(): boolean {
    return this.closedFlag;
  }

  get length(): number {
    return this.buffer.size;
  }

  get pendingSenders(): number {
    return this.sendQueue.length;
  }

  get pendingReceivers(): number {
    return this.recvQueue.length;
  }

  /** 发送一个值；必要时挂起，直到值被接收或进入缓冲区。 */
  async send(value: T): Promise<void> {
    if (this.closedFlag) {
      throw new Error("send on closed channel");
    }
    // 有等待的接收者：直接交接，精确唤醒队首接收协程。
    const receiver = this.recvQueue.shift();
    if (receiver !== undefined) {
      receiver.resolve({ value, done: false });
      return;
    }
    // 缓冲区有空位：入缓冲即完成。
    if (this.buffer.push(value)) {
      return;
    }
    // 缓冲区满（或无缓冲）：挂起当前协程，等待接收者唤醒。
    await new Promise<void>((resolve) => {
      this.sendQueue.push({ value, resolve });
    });
  }

  /** 接收一个值；必要时挂起，直到有值可取或通道关闭（EOF）。 */
  async recv(): Promise<RecvResult<T>> {
    // 缓冲区有数据：直接取出，并唤醒一个等待发送者填补空位。
    if (!this.buffer.isEmpty) {
      const value = this.buffer.shift() as T;
      const sender = this.sendQueue.shift();
      if (sender !== undefined) {
        // 此刻 recvQueue 必为空（缓冲区非空时不存在等待接收者），
        // 因此发送者的值直接补入缓冲区。
        this.buffer.push(sender.value);
        sender.resolve();
      }
      return { value, done: false };
    }
    // 缓冲区空但有等待发送者（无缓冲场景）：直接交接并唤醒之。
    const sender = this.sendQueue.shift();
    if (sender !== undefined) {
      sender.resolve();
      return { value: sender.value, done: false };
    }
    // 通道已关闭且无剩余数据：EOF。
    if (this.closedFlag) {
      return { value: undefined, done: true };
    }
    // 挂起当前协程，等待发送者或 close 唤醒。
    return new Promise<RecvResult<T>>((resolve) => {
      this.recvQueue.push({ resolve });
    });
  }

  /**
   * 关闭通道：广播唤醒所有等待中的接收协程（EOF）。
   * 缓冲区中已发送的数据仍可被正常接收，之后 recv 返回 EOF。
   * 重复调用为 no-op。
   */
  close(): void {
    if (this.closedFlag) return;
    this.closedFlag = true;
    const waiters = this.recvQueue.splice(0);
    for (const waiter of waiters) {
      waiter.resolve({ value: undefined, done: true });
    }
  }

  /** 支持 `for await (const v of ch)`，通道关闭且耗尽后迭代结束。 */
  async *[Symbol.asyncIterator](): AsyncGenerator<T> {
    for (;;) {
      const result = await this.recv();
      if (result.done) return;
      yield result.value;
    }
  }
}

/** 便捷构造：无缓冲 Channel。 */
export function chan<T>(): Channel<T>;
/** 便捷构造：容量为 capacity 的缓冲 Channel。 */
export function chan<T>(capacity: number): Channel<T>;
export function chan<T>(capacity = 0): Channel<T> {
  return new Channel<T>(capacity);
}
