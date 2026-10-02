import { RingBuffer } from './ring-buffer.ts';

/** `recv()` 的返回结果，对应 Go 的 `v, ok := <-ch`。 */
export interface RecvResult<T> {
  value: T | undefined;
  /** false 表示通道已关闭且缓冲区已排空（EOF）。 */
  ok: boolean;
}

/** 向已关闭的通道发送、或重复关闭时抛出（对应 Go 的 panic）。 */
export class ChannelClosedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ChannelClosedError';
  }
}

interface SendWaiter<T> {
  value: T;
  resolve: () => void;
  reject: (err: Error) => void;
}

interface RecvWaiter<T> {
  resolve: (result: RecvResult<T>) => void;
}

const EOF: RecvResult<never> = { value: undefined, ok: false };

/**
 * 仿 Go 语义的协程 Channel。
 *
 * - 支持带缓冲（capacity > 0）与无缓冲（capacity = 0，rendezvous）两种模式；
 * - 缓冲区使用无锁环形队列（见 ring-buffer.ts）；
 * - 通道满时 send 挂起当前协程、通道空时 recv 挂起当前协程；
 * - 等待协程进入 FIFO 队列，对端操作时精确唤醒队首的匹配协程（不广播、不惊群）；
 * - close() 向所有阻塞中的读协程广播 EOF，并唤醒所有阻塞中的写协程使其抛错。
 */
export class Channel<T> {
  private readonly buffer: RingBuffer<T>;
  private readonly sendQueue: SendWaiter<T>[] = [];
  private readonly recvQueue: RecvWaiter<T>[] = [];
  private closedFlag = false;
  public readonly capacity: number;

  constructor(capacity = 0) {
    this.capacity = capacity;
    this.buffer = new RingBuffer<T>(capacity);
  }

  get closed(): boolean {
    return this.closedFlag;
  }

  /** 当前阻塞中的发送/接收协程数（用于观测与测试）。 */
  get pendingSends(): number {
    return this.sendQueue.length;
  }

  get pendingRecvs(): number {
    return this.recvQueue.length;
  }

  /**
   * 发送一个值。通道满（或无缓冲且无等待接收者）时挂起当前协程，
   * 直到接收者取走值腾出位置后被精确唤醒。
   */
  async send(value: T): Promise<void> {
    if (this.closedFlag) {
      throw new ChannelClosedError('send on closed channel');
    }
    // 有直接等待的接收者：值不经缓冲区，直接交接（无缓冲 rendezvous 也走这里）。
    const receiver = this.recvQueue.shift();
    if (receiver) {
      receiver.resolve({ value, ok: true });
      return;
    }
    if (this.buffer.push(value)) {
      return;
    }
    // 缓冲区满：挂起当前协程，进入发送等待队列。
    return new Promise<void>((resolve, reject) => {
      this.sendQueue.push({ value, resolve, reject });
    });
  }

  /**
   * 接收一个值。通道空（且无等待发送者）时挂起当前协程，
   * 直到对端 send 或 close 时被唤醒。通道关闭且排空后返回 EOF（ok: false）。
   */
  async recv(): Promise<RecvResult<T>> {
    if (!this.buffer.isEmpty()) {
      const value = this.buffer.pop() as T;
      // 腾出位置后，唤醒队首阻塞发送者，把它的值补入缓冲区。
      const sender = this.sendQueue.shift();
      if (sender) {
        this.buffer.push(sender.value);
        sender.resolve();
      }
      return { value, ok: true };
    }
    // 缓冲区为空。若仍有阻塞发送者，说明是无缓冲通道：直接交接。
    const sender = this.sendQueue.shift();
    if (sender) {
      sender.resolve();
      return { value: sender.value, ok: true };
    }
    if (this.closedFlag) {
      return EOF;
    }
    // 挂起当前协程，进入接收等待队列。
    return new Promise<RecvResult<T>>((resolve) => {
      this.recvQueue.push({ resolve });
    });
  }

  /**
   * 关闭通道：
   * - 向所有阻塞中的接收协程广播 EOF；
   * - 唤醒所有阻塞中的发送协程并使其抛出 ChannelClosedError（同 Go panic）；
   * - 之后 recv 会先排空缓冲区再返回 EOF；send 直接抛错。
   */
  close(): void {
    if (this.closedFlag) {
      throw new ChannelClosedError('close of closed channel');
    }
    this.closedFlag = true;
    for (const receiver of this.recvQueue.splice(0)) {
      receiver.resolve(EOF);
    }
    for (const sender of this.sendQueue.splice(0)) {
      sender.reject(new ChannelClosedError('send on closed channel'));
    }
  }
}

/** 启动一个协程（等价于 Go 的 `go f()`）。 */
export function go(fn: () => Promise<void> | void): Promise<void> {
  return Promise.resolve().then(fn);
}
